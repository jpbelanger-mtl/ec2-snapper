package main

import (
	"flag"
	"strings"
	"time"

	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mitchellh/cli"
)

type CreateVolumeSnapshotCommand struct {
	Ui        cli.Ui
	AwsRegion string
	VolumeId  string
	DryRun    bool
	Wait      bool
}

// descriptions for args
var createVolumeSnapshotDscrAwsRegion = "The AWS region to use (e.g. us-west-2)"
var createVolumeSnapshotDscrVolumeId = "The id of the volume to snapshot"
var createVolumeSnapshotDscrWait = "Wait for snapshot to complete (default: false)"
var createVolumeSnapshotDscrDryRun = "Execute a simulated run"

func (c *CreateVolumeSnapshotCommand) Help() string {
	return `ec2-snapper create <args> [--help]

Create a EBS snapshot of the given EC2 volume.

Available args are:
--region     ` + createVolumeSnapshotDscrAwsRegion + `
--volume-id  ` + createVolumeSnapshotDscrVolumeId + `
--dry-run    ` + createVolumeSnapshotDscrDryRun + `
--wait       ` + createVolumeSnapshotDscrWait
}

func (c *CreateVolumeSnapshotCommand) Synopsis() string {
	return "Create a snapshot for a given volume"
}

func (c *CreateVolumeSnapshotCommand) Run(args []string) int {
	// Handle the command-line args
	cmdFlags := flag.NewFlagSet("createVolumeSnapshot", flag.ExitOnError)
	cmdFlags.Usage = func() { c.Ui.Output(c.Help()) }

	cmdFlags.StringVar(&c.AwsRegion, "region", "", createVolumeSnapshotDscrAwsRegion)
	cmdFlags.StringVar(&c.VolumeId, "volume-id", "", createVolumeSnapshotDscrVolumeId)
	cmdFlags.BoolVar(&c.DryRun, "dry-run", false, createVolumeSnapshotDscrDryRun)
	cmdFlags.BoolVar(&c.Wait, "wait", false, createVolumeSnapshotDscrWait)

	if err := cmdFlags.Parse(args); err != nil {
		return 1
	}

	if _, err := createVolumeSnapshot(*c); err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	return 0
}

func createVolumeSnapshot(c CreateVolumeSnapshotCommand) (string, error) {
	snapshotId := ""

	if err := validateCreateVolumeSnapshotArgs(c); err != nil {
		return snapshotId, err
	}

	session := session.New(&aws.Config{Region: &c.AwsRegion})
	svc := ec2.New(session)

	// Generate a nicely formatted timestamp for right now
	const dateLayoutForAmiName = "2006-01-02 at 15_04_05 (MST)"
	t := time.Now()

	// Create the EBS Snapshot
	description := "Automated snapshot by ec2-snapper at " + t.Format(dateLayoutForAmiName)
	c.Ui.Output("==> Creating Snapshot for " + c.VolumeId + "...")

	resp, err := svc.CreateSnapshot(&ec2.CreateSnapshotInput{
		VolumeId:    &c.VolumeId,
		DryRun:      &c.DryRun,
		Description: &description,
	})
	if err != nil && strings.Contains(err.Error(), "NoCredentialProviders") {
		return snapshotId, errors.New("ERROR: No AWS credentials were found.  Either set the environment variables AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or run this program on an EC2 instance that has an IAM Role with the appropriate permissions.")
	} else if err != nil {
		return snapshotId, err
	}

	snapshotId = *resp.SnapshotId

	if !c.DryRun {
		snapshot, err := getSnapshot(*svc, snapshotId)
		if err != nil {
			return snapshotId, err
		}
		c.Ui.Output("      State " + *snapshot.State)

		// If the status is failed throw an error
		if *snapshot.State == ec2.SnapshotStateError {
			return snapshotId, errors.New("ERROR: Snapshot was created but entered a state of 'Error'. This is an AWS issue. Please re-run this command.  Note that you will need to manually de-register the snapshot in the AWS console or via the API.")
		}

		if c.Wait {
			tick, _ := time.ParseDuration(fmt.Sprintf("%vms", 10000))
			timer := time.NewTicker(tick)
		OUT:
			for {
				select {
				case <-timer.C:
					snapshot, err = getSnapshot(*svc, snapshotId)
					if err != nil {
						return snapshotId, err
					}
					c.Ui.Output("      Progress " + *snapshot.Progress)
					if *snapshot.State == ec2.SnapshotStateCompleted {
						c.Ui.Output("      State " + *snapshot.State)
						break OUT
					}
				}
			}

		}
	}

	// Announce success
	c.Ui.Info("==> Success! Created " + snapshotId)
	return snapshotId, nil
}

func validateCreateVolumeSnapshotArgs(c CreateVolumeSnapshotCommand) error {
	if c.AwsRegion == "" {
		return errors.New("ERROR: The argument '--region' is required.")
	}

	if c.VolumeId == "" {
		return errors.New("ERROR: The argument '--volume-id' is required.")
	}

	return nil
}

func getSnapshot(svc ec2.EC2, snapshotId string) (*ec2.Snapshot, error) {
	snapshotsResp, err := svc.DescribeSnapshots(&ec2.DescribeSnapshotsInput{
		SnapshotIds: []*string{&snapshotId},
	})
	if err != nil {
		return nil, err
	}

	// If no snapshot at all was found, throw an error
	if len(snapshotsResp.Snapshots) == 0 {
		return nil, errors.New("ERROR: Could not find the Snapshot just created.")
	}

	return snapshotsResp.Snapshots[0], nil
}

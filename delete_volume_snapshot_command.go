package main

import (
	"errors"
	"flag"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mitchellh/cli"
)

type DeleteVolumeSnapshotCommand struct {
	Ui             cli.Ui
	AwsRegion      string
	VolumeId       string
	OlderThan      string
	RequireAtLeast int
	DryRun         bool
}

// descriptions for args
var deleteVolumeSnapshotDscrAwsRegion = "The AWS region to use (e.g. us-west-2)"
var deleteVolumeSnapshotDscrVolumeId = "The ID of the EC2 volume from which the snapshot to be deleted were originally created."
var deleteVolumeSnapshotOlderThan = "Delete snapshots older than the specified time; accepts formats like '30d' or '4h'."
var deleteVolumeSnapshotRequireAtLeast = "Never delete snapshots such that fewer than this number  will remain. E.g. require at least 3 snapshot remain."
var deleteVolumeSnapshotDscrDryRun = "Execute a simulated run. Lists snapshots to be deleted, but does not actually delete them."

func (c *DeleteVolumeSnapshotCommand) Help() string {
	return `ec2-snapper create <args> [--help]

Delete a EBS Snapshot of the given EC2 volume.

Available args are:
--region           ` + deleteVolumeSnapshotDscrAwsRegion + `
--volume-id        ` + deleteVolumeSnapshotDscrVolumeId + `
--older-than       ` + deleteVolumeSnapshotOlderThan + `
--require-at-least ` + deleteVolumeSnapshotRequireAtLeast + `
--dry-run          ` + deleteVolumeSnapshotDscrDryRun
}

func (c *DeleteVolumeSnapshotCommand) Synopsis() string {
	return "Delete the specified snapshots"
}

func (c *DeleteVolumeSnapshotCommand) Run(args []string) int {

	// Handle the command-line args
	cmdFlags := flag.NewFlagSet("deleteVolumeSnapshot", flag.ExitOnError)
	cmdFlags.Usage = func() {
		c.Ui.Output(c.Help())
	}

	cmdFlags.StringVar(&c.AwsRegion, "region", "", deleteDscrAwsRegion)
	cmdFlags.StringVar(&c.VolumeId, "volume-id", "", deleteDscrInstanceId)
	cmdFlags.StringVar(&c.OlderThan, "older-than", "", deleteOlderThan)
	cmdFlags.IntVar(&c.RequireAtLeast, "require-at-least", 0, requireAtLeast)
	cmdFlags.BoolVar(&c.DryRun, "dry-run", false, deleteDscrDryRun)

	if err := cmdFlags.Parse(args); err != nil {
		return 1
	}

	if err := deleteVolumeSnapshotSnapshots(*c); err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	return 0
}

func deleteVolumeSnapshotSnapshots(c DeleteVolumeSnapshotCommand) error {
	if err := validateDeleteVolumeSnapshotsArgs(c); err != nil {
		return err
	}

	if c.DryRun {
		c.Ui.Warn("WARNING: This is a dry run, and no actions will be taken, despite what any output may say!")
	}

	session := session.New(&aws.Config{Region: &c.AwsRegion})
	svc := ec2.New(session)

	c.Ui.Info("Fetching snapshots for volume-id: " + c.VolumeId)
	describeSnapshotsResp, err := svc.DescribeSnapshots(&ec2.DescribeSnapshotsInput{
		Filters: []*ec2.Filter{
			&ec2.Filter{
				Name:   aws.String("volume-id"),
				Values: []*string{&c.VolumeId},
			},
		},
	})
	if err != nil {
		return err
	}

	snapshots := describeSnapshotsResp.Snapshots
	c.Ui.Info("==> Found " + strconv.Itoa(len(snapshots)) + " snapshots")
	if len(snapshots) == 0 {
		c.Ui.Info("NO ACTION TAKEN. There are no existing snapshots of volume " + c.VolumeId + " to delete.")
		return nil
	}

	// Check that at least the --require-at-least number of snapshots exists
	// - Note that even if this passes, we still want to avoid deleting so many snapshot that we go below the threshold
	if len(describeSnapshotsResp.Snapshots) <= c.RequireAtLeast {
		c.Ui.Info("NO ACTION TAKEN. There are currently " + strconv.Itoa(len(describeSnapshotsResp.Snapshots)) + " snapshot(s), and --require-at-least=" + strconv.Itoa(c.RequireAtLeast) + " so no further action can be taken.")
		return nil
	}

	c.Ui.Info("Keeping only snapshots that are at least " + c.OlderThan)
	hours, err := parseOlderThanToHours(c.OlderThan)
	if err != nil {
		return err
	}

	filteredSnapshots, err := filterSnapshotsByDateRange(snapshots, hours)
	if err != nil {
		return err
	}
	c.Ui.Output("==> Found " + strconv.Itoa(len(filteredSnapshots)) + " total snapshots for deletion.")

	if len(filteredSnapshots) == 0 {
		c.Ui.Warn("No snapshot to delete.")
		return nil
	}

	if err := deleteVolumeSnapshots(snapshots, svc, c.DryRun, c.Ui); err != nil {
		return err
	}

	if c.DryRun {
		c.Ui.Info("==> DRY RUN. Had this not been a dry run, " + strconv.Itoa(len(filteredSnapshots)) + " snapshots would have been deleted.")
	} else {
		c.Ui.Info("==> Success! Deleted " + strconv.Itoa(len(filteredSnapshots)) + " snapshots.")
	}
	return nil
}

// Now filter the snapshots to only include those within our date range
func filterSnapshotsByDateRange(snapshots []*ec2.Snapshot, olderThanHours float64) ([]*ec2.Snapshot, error) {
	var filteredSnapshots []*ec2.Snapshot

	for i := 0; i < len(snapshots); i++ {
		now := time.Now()

		duration := now.Sub(*snapshots[i].StartTime)

		if duration.Hours() > olderThanHours {
			filteredSnapshots = append(filteredSnapshots, snapshots[i])
		}
	}

	return filteredSnapshots, nil
}

func deleteVolumeSnapshots(snapshots []*ec2.Snapshot, svc *ec2.EC2, dryRun bool, ui cli.Ui) error {
	// Delete all snapshots that were found
	ui.Output(" Found " + strconv.Itoa(len(snapshots)) + " snapshot(s) to delete")
	for _, snapshot := range snapshots {
		ui.Output("   Deleting snapshot " + *snapshot.SnapshotId + "...")
		_, deleteErr := svc.DeleteSnapshot(&ec2.DeleteSnapshotInput{
			DryRun:     &dryRun,
			SnapshotId: snapshot.SnapshotId,
		})

		if deleteErr != nil {
			return deleteErr
		}
	}

	ui.Output("   Done...")

	return nil
}

// Check for required command-line args
func validateDeleteVolumeSnapshotsArgs(c DeleteVolumeSnapshotCommand) error {
	if c.AwsRegion == "" {
		return errors.New("ERROR: The argument '--region' is required.")
	}

	if c.VolumeId == "" {
		return errors.New("ERROR: The argument '--volume-id' is required'.")
	}

	if c.OlderThan == "" {
		return errors.New("ERROR: The argument '--older-than' is required.")
	}

	if c.RequireAtLeast < 0 {
		return errors.New("ERROR: The argument '--require-at-least' must be a positive integer.")
	}

	return nil
}

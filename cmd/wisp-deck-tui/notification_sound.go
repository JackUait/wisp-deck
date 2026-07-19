package main

import (
	"context"
	"fmt"

	"github.com/jackuait/wisp-deck/internal/soundpref"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newNotificationSoundCommand(playNotificationSound))
}

func newNotificationSoundCommand(play func(string) error) *cobra.Command {
	var features string
	cmd := &cobra.Command{
		Use:           "notification-sound --features-file PATH",
		Short:         "Play the configured notification sound",
		Hidden:        true,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(*cobra.Command, []string) error {
			if play == nil {
				return fmt.Errorf("notification sound player is unavailable")
			}
			return withConfiguredNotificationSound(features, play)
		},
	}
	cmd.Flags().StringVar(&features, "features-file", "", "notification sound features file")
	_ = cmd.MarkFlagRequired("features-file")
	return cmd
}

func withConfiguredNotificationSound(
	features string,
	play func(string) error,
) error {
	if play == nil {
		return fmt.Errorf("notification sound player is unavailable")
	}
	return soundpref.WithExclusiveLock(features, func() error {
		sound := soundpref.Read(features)
		if sound == "" {
			return nil
		}
		return play(sound)
	})
}

func playNotificationSound(name string) error {
	effect, ok := newSystemSoundHostEffect(name)
	if !ok {
		return nil
	}
	return runHostEffect(context.Background(), effect)
}

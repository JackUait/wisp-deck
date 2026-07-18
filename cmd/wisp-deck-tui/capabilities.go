package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

type binaryCapabilities struct {
	HostEffectsCompiled     bool   `json:"host_effects_compiled"`
	SoundPreviewCompiled    bool   `json:"sound_preview_compiled"`
	HostEffectsBoundary     int    `json:"host_effects_boundary"`
	HostEffectsAllowed      bool   `json:"host_effects_allowed"`
	HostEffectsDenialReason string `json:"host_effects_denial_reason,omitempty"`
}

func init() {
	rootCmd.AddCommand(newCapabilitiesCommand(currentBinaryCapabilities))
}

func currentBinaryCapabilities() binaryCapabilities {
	decision := currentHostEffectsDecision()
	return binaryCapabilities{
		HostEffectsCompiled:     HostEffectsCapability == "enabled",
		SoundPreviewCompiled:    SoundPreviewCapability == "enabled",
		HostEffectsBoundary:     HostEffectsBoundaryVersion,
		HostEffectsAllowed:      decision.Allowed,
		HostEffectsDenialReason: decision.DenialReason,
	}
}

func newCapabilitiesCommand(
	readCapabilities func() binaryCapabilities,
) *cobra.Command {
	requireProduction := false
	command := &cobra.Command{
		Use:          "capabilities",
		Short:        "Report machine-readable binary capabilities",
		Hidden:       true,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(command *cobra.Command, _ []string) error {
			capabilities := readCapabilities()
			if err := json.NewEncoder(command.OutOrStdout()).Encode(capabilities); err != nil {
				return fmt.Errorf("encode capabilities: %w", err)
			}
			if requireProduction &&
				(!capabilities.HostEffectsCompiled ||
					!capabilities.SoundPreviewCompiled ||
					capabilities.HostEffectsBoundary != 1) {
				return fmt.Errorf("binary is missing required production capabilities")
			}
			return nil
		},
	}
	command.Flags().BoolVar(
		&requireProduction,
		"require-production",
		false,
		"fail unless the artifact contains the production host-effect boundary",
	)
	return command
}

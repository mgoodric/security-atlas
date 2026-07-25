package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
)

func newRegisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "register",
		Short:         "register this connector instance with the platform",
		PreRunE:       func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, _ []string) error {
			client, conn, err := dialConnectorRegistry()
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()

			ctx, cancel := authedContext(10 * time.Second)
			defer cancel()

			// profiles_supported = [pull, subscribe]: the connector retrieves data
			// FROM Jamf Pro either on a schedule (read-only API GETs — pull) or
			// event-driven via Jamf webhook deliveries (subscribe). The platform-side
			// wire is always push (invariant #3) regardless of either value.
			resp, err := client.Register(ctx, &connectorsv1.RegisterRequest{
				Name:              ConnectorName,
				Version:           connectorVersion(),
				InstanceId:        uuid.NewString(),
				SupportedKinds:    SupportedKinds,
				ProfilesSupported: ProfilesSupported,
			})
			if err != nil {
				return fmt.Errorf("register: %w", err)
			}
			fmt.Printf("registered id=%s instance_id=%s kinds=%d profiles=%s\n",
				resp.GetHandle().GetId(), resp.GetHandle().GetInstanceId(), len(SupportedKinds), strings.Join(ProfilesSupported, ","))
			return nil
		},
	}
}

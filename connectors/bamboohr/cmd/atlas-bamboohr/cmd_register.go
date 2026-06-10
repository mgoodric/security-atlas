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
			// FROM BambooHR two ways — a scheduled read-only poll (pull) and an
			// event-driven webhook the connector receives source-side (subscribe,
			// slice 573). BOTH describe how the connector retrieves from the source;
			// the platform-side wire is ALWAYS push (invariant #3) regardless.
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

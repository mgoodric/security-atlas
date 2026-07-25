package main

import (
	"fmt"
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

			// profiles_supported = [pull, subscribe]: BOTH describe how the connector
			// retrieves data FROM the cluster — `pull` is a scheduled read-and-push
			// pass (read-only API list calls); `subscribe` is event-driven via the
			// Kubernetes watch API (the 'subscribe' subcommand consumes a long-lived
			// watch stream and pushes as RBAC / workload changes happen — NOT
			// "continuous monitoring", the mechanism is named honestly). The
			// platform-side wire is ALWAYS push (invariant #3) regardless of profile.
			resp, err := client.Register(ctx, &connectorsv1.RegisterRequest{
				Name:              ConnectorName,
				Version:           connectorVersion(),
				InstanceId:        uuid.NewString(),
				SupportedKinds:    SupportedKinds,
				ProfilesSupported: []string{"pull", "subscribe"},
			})
			if err != nil {
				return fmt.Errorf("register: %w", err)
			}
			fmt.Printf("registered id=%s instance_id=%s kinds=%d profiles=pull,subscribe\n",
				resp.GetHandle().GetId(), resp.GetHandle().GetInstanceId(), len(SupportedKinds))
			return nil
		},
	}
}

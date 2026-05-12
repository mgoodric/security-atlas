package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/manual/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/manual/internal/manualsftp"
)

type sftpFlags struct {
	host        string
	port        int
	user        string
	pathGlob    string
	keyFile     string
	knownHosts  string
	controlID   string
	scope       []string
	dialTimeout time.Duration
}

func newSFTPCmd() *cobra.Command {
	var f sftpFlags
	cmd := &cobra.Command{
		Use:           "sftp",
		Short:         "pull files matching a path glob from SFTP; emit one manual.upload.v1 record per file",
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.host == "" {
				return errors.New("--host is required")
			}
			if f.user == "" {
				return errors.New("--user is required")
			}
			if f.pathGlob == "" {
				return errors.New("--path is required (glob)")
			}
			if f.keyFile == "" {
				return errors.New("--key-file is required (SSH key is loaded from disk, never a flag value)")
			}
			if f.knownHosts == "" {
				return errors.New("--known-hosts is required (host-key verification is mandatory)")
			}
			if len(f.scope) == 0 {
				return errors.New("at least one --scope key=value pair is required")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doSFTP(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.host, "host", "", "SFTP host [required]")
	cmd.Flags().IntVar(&f.port, "port", 22, "SFTP port")
	cmd.Flags().StringVar(&f.user, "user", "", "SFTP user [required]")
	cmd.Flags().StringVar(&f.pathGlob, "path", "", "SFTP file path glob (e.g. /inbox/*.pdf) [required]")
	cmd.Flags().StringVar(&f.keyFile, "key-file", "", "path to SSH private key file [required]")
	cmd.Flags().StringVar(&f.knownHosts, "known-hosts", "", "path to known_hosts file [required]")
	cmd.Flags().StringVar(&f.controlID, "control-id", "scf:GOV-04", "control id to attach to each record")
	cmd.Flags().StringArrayVar(&f.scope, "scope", nil, "scope tag in key=value form (repeatable, at least one required)")
	cmd.Flags().DurationVar(&f.dialTimeout, "dial-timeout", 15*time.Second, "SFTP dial timeout")
	return cmd
}

func doSFTP(ctx context.Context, f sftpFlags) error {
	keyBytes, err := manualsftp.LoadPrivateKey(f.keyFile)
	if err != nil {
		return err
	}
	hostCB, err := manualsftp.NewHostKeyCallback(f.knownHosts)
	if err != nil {
		return err
	}
	cfg, err := manualsftp.BuildSSHConfig(manualsftp.BuildOpts{
		User:            f.user,
		PrivateKeyPEM:   keyBytes,
		HostKeyCallback: hostCB,
	})
	if err != nil {
		return err
	}
	cfg.Timeout = f.dialTimeout

	addr := net.JoinHostPort(f.host, strconv.Itoa(f.port))
	sshClient, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial: %w", err)
	}
	defer func() { _ = sshClient.Close() }()

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("sftp client: %w", err)
	}
	defer func() { _ = sftpClient.Close() }()

	matches, err := sftpClient.Glob(f.pathGlob)
	if err != nil {
		return fmt.Errorf("sftp glob: %w", err)
	}

	scope, err := parseScope(f.scope)
	if err != nil {
		return err
	}

	client, err := sdk.NewClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = client.Close() }()

	now := time.Now().UTC().Truncate(time.Hour)
	pushed := 0
	for _, p := range matches {
		stat, err := sftpClient.Stat(p)
		if err != nil {
			return fmt.Errorf("stat %q: %w", p, err)
		}
		if stat.IsDir() {
			continue
		}
		rec, err := buildSFTPRecord(f.host, p, stat.Size(), stat.ModTime(), f.controlID, scope, now)
		if err != nil {
			return fmt.Errorf("build record %q: %w", p, err)
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = client.Push(pctx, rec)
		cancel()
		if err != nil {
			return fmt.Errorf("push %q: %w", p, err)
		}
		pushed++
	}
	fmt.Printf("pushed %d records (host=%s path=%s)\n", pushed, f.host, f.pathGlob)
	return nil
}

func buildSFTPRecord(host, path string, size int64, mtime time.Time, controlID string, scope []*evidencev1.ScopeDimension, observedAt time.Time) (*evidencev1.EvidenceRecord, error) {
	payload, err := structpb.NewStruct(map[string]any{
		"uploaded_by":  actorID("sftp"),
		"filename":     filepath.Base(path),
		"content_type": guessContentType(path),
		"size_bytes":   float64(size),
		"description":  fmt.Sprintf("sftp://%s%s", host, path),
		"host":         host,
		"remote_path":  path,
		"mtime":        mtime.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.SFTPFileKey(host, path, mtime),
		EvidenceKind:   "manual.upload.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope:          scope,
		ObservedAt:     timestamppb.New(observedAt),
		Result:         evidencev1.Result_RESULT_INCONCLUSIVE,
		Payload:        payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("sftp"),
		},
	}, nil
}

// guessContentType returns a reasonable Content-Type for the file's
// extension. Falls back to application/octet-stream so the schema's
// required content_type field is always non-empty.
func guessContentType(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	case ".pdf":
		return "application/pdf"
	case ".txt", ".log":
		return "text/plain"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	default:
		return "application/octet-stream"
	}
}

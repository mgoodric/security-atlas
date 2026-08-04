package personnelsecurity

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

func TestWorkflowFromWorkerMapsJoinerAndLeaver(t *testing.T) {
	if got, ok := workflowFromWorker(worker.Worker{Status: worker.StatusPending}); !ok || got != WorkflowOnboarding {
		t.Fatalf("pending worker = %q/%v, want onboarding/true", got, ok)
	}
	if got, ok := workflowFromWorker(worker.Worker{Status: worker.StatusTerminated}); !ok || got != WorkflowOffboarding {
		t.Fatalf("terminated worker = %q/%v, want offboarding/true", got, ok)
	}
	if _, ok := workflowFromWorker(worker.Worker{Status: worker.StatusOnLeave}); ok {
		t.Fatalf("on_leave worker should not create a checklist")
	}
}

func TestDefaultChecklistTemplatesTrackAccessEvidence(t *testing.T) {
	onboarding := itemTemplates(WorkflowOnboarding)
	if len(onboarding) == 0 || onboarding[1].Slug != "provision_access" {
		t.Fatalf("onboarding templates should include access provisioning")
	}
	offboarding := itemTemplates(WorkflowOffboarding)
	if len(offboarding) == 0 || offboarding[1].Slug != "revoke_access" {
		t.Fatalf("offboarding templates should include access revocation")
	}
}

func TestDefaultDueAt(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if got := defaultDueAt(WorkflowOffboarding, now, now); !got.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("offboarding due = %s", got)
	}
	if got := defaultDueAt(WorkflowOnboarding, now, now); !got.Equal(now.Add(7 * 24 * time.Hour)) {
		t.Fatalf("onboarding due = %s", got)
	}
}

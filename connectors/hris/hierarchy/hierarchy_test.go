package hierarchy

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

// rosterFrom builds a normalized roster from compact (id, manager, status)
// triples so the tests read as a tree, not a struct dump.
func rosterFrom(triples ...[3]string) []worker.Worker {
	out := make([]worker.Worker, 0, len(triples))
	for _, t := range triples {
		status := worker.EmploymentStatus(t[2])
		if status == "" {
			status = worker.StatusActive
		}
		out = append(out, worker.Worker{
			WorkerID:            t[0],
			ManagerAssignmentID: t[1],
			Status:              status,
			ObservedAt:          time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		})
	}
	return out
}

func edgeByID(edges []Edge, id string) (Edge, bool) {
	for _, e := range edges {
		if e.WorkerAssignmentID == id {
			return e, true
		}
	}
	return Edge{}, false
}

func TestBuild_DerivesDepthFromManagerChain(t *testing.T) {
	t.Parallel()
	// ceo <- vp <- ic   (ic reports to vp reports to ceo, ceo is root)
	edges := Build(rosterFrom(
		[3]string{"ceo", "", "active"},
		[3]string{"vp", "ceo", "active"},
		[3]string{"ic", "vp", "active"},
	))
	want := map[string]int{"ceo": 0, "vp": 1, "ic": 2}
	for id, d := range want {
		e, ok := edgeByID(edges, id)
		if !ok {
			t.Fatalf("missing edge %q", id)
		}
		if e.Depth != d {
			t.Errorf("%s depth = %d; want %d", id, e.Depth, d)
		}
		if e.OrphanedReport || e.CycleMember {
			t.Errorf("%s should be neither orphaned nor a cycle member: %+v", id, e)
		}
	}
}

func TestBuild_RootHasEmptyManagerAndDepthZero(t *testing.T) {
	t.Parallel()
	edges := Build(rosterFrom([3]string{"ceo", "", "active"}))
	e, _ := edgeByID(edges, "ceo")
	if e.ManagerAssignmentID != "" || e.Depth != 0 {
		t.Errorf("root edge = %+v; want empty manager + depth 0", e)
	}
}

// TestBuild_OrphanedWhenManagerTerminated is the orphaned-report case: a worker
// whose manager exists but is terminated (the leaver signal) cannot route an
// access review — the approver chain is broken.
func TestBuild_OrphanedWhenManagerTerminated(t *testing.T) {
	t.Parallel()
	edges := Build(rosterFrom(
		[3]string{"mgr", "", "terminated"},
		[3]string{"report", "mgr", "active"},
	))
	e, _ := edgeByID(edges, "report")
	if !e.OrphanedReport {
		t.Errorf("report -> terminated mgr should be orphaned: %+v", e)
	}
}

// TestBuild_OrphanedWhenManagerAbsent: a worker whose manager id is not in the
// roster at all is also orphaned (the manager was removed from the directory).
func TestBuild_OrphanedWhenManagerAbsent(t *testing.T) {
	t.Parallel()
	edges := Build(rosterFrom([3]string{"report", "ghost", "active"}))
	e, _ := edgeByID(edges, "report")
	if !e.OrphanedReport {
		t.Errorf("report -> absent mgr should be orphaned: %+v", e)
	}
}

func TestBuild_ActiveManagerIsNotOrphaned(t *testing.T) {
	t.Parallel()
	edges := Build(rosterFrom(
		[3]string{"mgr", "", "active"},
		[3]string{"report", "mgr", "active"},
	))
	e, _ := edgeByID(edges, "report")
	if e.OrphanedReport {
		t.Errorf("report -> active mgr should NOT be orphaned: %+v", e)
	}
}

// TestBuild_TwoCycleTerminates is the LOAD-BEARING cycle-termination case: a
// manager cycle A -> B -> A must NOT loop forever. Both nodes are flagged
// CycleMember with undefined depth, and Build returns.
func TestBuild_TwoCycleTerminates(t *testing.T) {
	t.Parallel()
	done := make(chan []Edge, 1)
	go func() {
		done <- Build(rosterFrom(
			[3]string{"a", "b", "active"},
			[3]string{"b", "a", "active"},
		))
	}()
	select {
	case edges := <-done:
		for _, id := range []string{"a", "b"} {
			e, _ := edgeByID(edges, id)
			if !e.CycleMember {
				t.Errorf("%s should be flagged CycleMember: %+v", id, e)
			}
			if e.Depth != -1 {
				t.Errorf("%s on a cycle should have undefined depth -1; got %d", id, e.Depth)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Build did not terminate on a manager cycle (A->B->A) — cycle detection failed")
	}
}

// TestBuild_SelfManagerCycleTerminates: a self-manager edge (A -> A) is a
// degenerate cycle and must also terminate + flag.
func TestBuild_SelfManagerCycleTerminates(t *testing.T) {
	t.Parallel()
	done := make(chan []Edge, 1)
	go func() { done <- Build(rosterFrom([3]string{"a", "a", "active"})) }()
	select {
	case edges := <-done:
		e, _ := edgeByID(edges, "a")
		if !e.CycleMember || e.Depth != -1 {
			t.Errorf("self-manager should be CycleMember with depth -1: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Build did not terminate on a self-manager cycle (A->A)")
	}
}

// TestBuild_LongCycleTerminates: a 3-ring A->B->C->A terminates and flags all
// three.
func TestBuild_LongCycleTerminates(t *testing.T) {
	t.Parallel()
	done := make(chan []Edge, 1)
	go func() {
		done <- Build(rosterFrom(
			[3]string{"a", "b", "active"},
			[3]string{"b", "c", "active"},
			[3]string{"c", "a", "active"},
		))
	}()
	select {
	case edges := <-done:
		for _, id := range []string{"a", "b", "c"} {
			e, _ := edgeByID(edges, id)
			if !e.CycleMember {
				t.Errorf("%s should be a cycle member in a 3-ring: %+v", id, e)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Build did not terminate on a 3-node manager ring")
	}
}

// TestBuild_NodeBelowCycleIsNotMislabeledCycleMember: a worker reporting INTO a
// cycle is not itself on the cycle (it is not part of the ring), but its depth
// is undefined because the chain never reaches a root.
func TestBuild_ChainIntoCycleHasUndefinedDepth(t *testing.T) {
	t.Parallel()
	edges := Build(rosterFrom(
		[3]string{"a", "b", "active"},
		[3]string{"b", "a", "active"},
		[3]string{"leaf", "a", "active"}, // reports to a cycle member
	))
	e, _ := edgeByID(edges, "leaf")
	if e.CycleMember {
		t.Errorf("leaf is below the cycle, not on it: %+v", e)
	}
	if e.Depth != -1 {
		t.Errorf("leaf chain enters a cycle -> depth undefined (-1); got %d", e.Depth)
	}
}

func TestBuild_DeterministicOrderByWorkerID(t *testing.T) {
	t.Parallel()
	edges := Build(rosterFrom(
		[3]string{"c", "", "active"},
		[3]string{"a", "", "active"},
		[3]string{"b", "", "active"},
	))
	if len(edges) != 3 || edges[0].WorkerAssignmentID != "a" || edges[1].WorkerAssignmentID != "b" || edges[2].WorkerAssignmentID != "c" {
		t.Errorf("edges not sorted by worker id: %+v", edges)
	}
}

func TestBuild_DropsWorkersMissingID(t *testing.T) {
	t.Parallel()
	edges := Build([]worker.Worker{{WorkerID: "", ManagerAssignmentID: "x"}, {WorkerID: "keep"}})
	if len(edges) != 1 || edges[0].WorkerAssignmentID != "keep" {
		t.Errorf("missing-id worker should be dropped: %+v", edges)
	}
}

func TestBuild_EmptyRosterYieldsEmpty(t *testing.T) {
	t.Parallel()
	if got := Build(nil); len(got) != 0 {
		t.Errorf("empty roster -> empty edges; got %+v", got)
	}
}

// TestEdge_HasNoPIIField is the structural over-collection guard (the slice-491
// identity boundary, extended to the hierarchy surface): the Edge struct must
// have NO field capable of holding a manager's (or any worker's) name, email,
// phone, address, or any personal contact / sensitive-PII detail. A field
// accidentally named "ManagerName" / "Email" / "Phone" trips this immediately —
// the type system is the first line of the guard, this reflection assertion is
// the executable proof.
func TestEdge_HasNoPIIField(t *testing.T) {
	t.Parallel()
	banned := []string{
		"name", "email", "phone", "mobile", "cell", "address", "street",
		"zip", "postal", "ssn", "nationalid", "national_id", "salary",
		"compensation", "comp", "pay", "wage", "bonus", "bank", "account",
		"routing", "iban", "benefit", "health", "insurance", "performance",
		"rating", "review", "dob", "birth", "gender", "ethnicity", "race",
		"contact", "personal",
	}
	typ := reflect.TypeOf(Edge{})
	for i := 0; i < typ.NumField(); i++ {
		fieldName := strings.ToLower(typ.Field(i).Name)
		for _, b := range banned {
			if strings.Contains(fieldName, b) {
				t.Errorf("Edge has field %q matching banned PII concept %q (slice-491 identity boundary)", typ.Field(i).Name, b)
			}
		}
	}
}

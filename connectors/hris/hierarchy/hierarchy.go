// Package hierarchy derives the manager reporting tree from the bounded HRIS
// worker roster (slice 571). Each worker.Worker already carries its direct
// manager's OPAQUE assignment id (worker.ManagerAssignmentID); this package
// turns that flat set of (worker -> manager) edges into per-worker hierarchy
// facts that access-review routing needs: the depth of each worker in the tree,
// whether the worker is orphaned (reports to a terminated or absent manager),
// and whether the worker sits on a manager cycle (A -> B -> A).
//
// The load-bearing guard (P0-491-3 / the slice-491 identity boundary, extended):
// an Edge carries OPAQUE assignment ids and derived booleans/ints ONLY. It has
// NO field — and no place to put a field — for a manager's (or any worker's)
// name, email, phone, address, or any other personal contact detail. The type
// system itself is the over-collection defence: a leak would be a compile error,
// and the structural reflection test (TestEdge_HasNoPIIField) makes the absence
// an executable assertion. The hierarchy is derived purely from the opaque
// worker_id / manager_assignment_id pair the roster already carries — it reads
// nothing new from the HRIS source.
//
// Boundedness: the roster is already bounded per run (worker.Normalize returns a
// finite slice from one bounded directory read). The tree walk visits each
// worker once; a manager cycle (A -> B -> A, or a self-manager A -> A) is
// detected and terminates rather than looping forever (cycleMember marks the
// involved workers; the walk never revisits a node already on the current path
// without stopping).
package hierarchy

import (
	"sort"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

// Edge is one worker's place in the reporting tree. Every field is an opaque
// assignment id or a derived fact. There is intentionally NO Name / Email /
// Phone / Address / Manager* contact field on this struct (the slice-491
// identity boundary): the only identities are opaque assignment ids.
type Edge struct {
	// WorkerAssignmentID is the OPAQUE worker id (the roster's stable key).
	WorkerAssignmentID string
	// ManagerAssignmentID is the OPAQUE worker id of this worker's direct
	// manager. Empty means the worker has no manager edge (a tree root, e.g. the
	// CEO).
	ManagerAssignmentID string
	// Depth is the worker's distance from a tree root (root = 0). A worker on a
	// cycle, or whose chain hits a cycle, has Depth -1 (undefined).
	Depth int
	// OrphanedReport is true when ManagerAssignmentID is set but resolves to a
	// manager that is absent from the roster OR is present but terminated — the
	// access-review approver chain is broken and the worker's review cannot route.
	OrphanedReport bool
	// CycleMember is true when the worker sits on a manager cycle (including a
	// self-manager edge). Such a worker has no well-defined approver chain; the
	// access-review routing must surface it for manual repair.
	CycleMember bool
}

// node is the minimal per-worker view Build needs: the worker's manager edge +
// whether the worker is terminated (the leaver signal that orphans that
// worker's reports). Presence is conveyed by the map lookup's ok return, so no
// explicit field is needed.
type node struct {
	managerID  string
	terminated bool
}

// Build derives the reporting tree from a bounded, normalized roster. The input
// is the same []worker.Worker the lifecycle push iterates; Build reads only each
// worker's opaque WorkerID + ManagerAssignmentID + Status (to detect terminated
// managers). The output is deterministically ordered by WorkerAssignmentID so
// the emitted evidence is stable run-to-run.
func Build(roster []worker.Worker) []Edge {
	nodes := make(map[string]node, len(roster))
	for _, w := range roster {
		if w.WorkerID == "" {
			continue
		}
		nodes[w.WorkerID] = node{
			managerID:  w.ManagerAssignmentID,
			terminated: w.Status == worker.StatusTerminated,
		}
	}

	// cycleMembers is the set of worker ids that sit on (or whose chain enters) a
	// manager cycle. Computed once via an explicit path-walk per worker with a
	// memoized colour map so the whole pass is O(N).
	cycleMembers := detectCycles(nodes)

	out := make([]Edge, 0, len(nodes))
	for id, n := range nodes {
		e := Edge{
			WorkerAssignmentID:  id,
			ManagerAssignmentID: n.managerID,
		}
		switch {
		case cycleMembers[id]:
			e.CycleMember = true
			e.Depth = -1
		case n.managerID == "":
			e.Depth = 0 // tree root
		default:
			mgr, ok := nodes[n.managerID]
			if !ok || mgr.terminated {
				e.OrphanedReport = true
			}
			e.Depth = depthOf(id, nodes, cycleMembers)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkerAssignmentID < out[j].WorkerAssignmentID })
	return out
}

// detectCycles returns the set of worker ids that lie on a manager cycle (or
// whose manager chain leads into one). Uses an iterative three-colour walk:
// white (unvisited), grey (on the current path), black (settled, acyclic). A
// grey node re-encountered on the same path is a cycle; every grey node on that
// path is a cycle member. This terminates on ANY input — a self-manager edge
// (A -> A), a 2-cycle (A -> B -> A), or a long ring — because each node is
// coloured at most twice.
func detectCycles(nodes map[string]node) map[string]bool {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	colour := make(map[string]int, len(nodes))
	cycle := make(map[string]bool)

	// Deterministic iteration order so cycle membership is stable.
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, start := range ids {
		if colour[start] != white {
			continue
		}
		// Walk the manager chain from start, recording the path. Stop when we hit
		// a settled node, a missing/empty manager (acyclic tail), or a grey node
		// already on THIS path (a cycle).
		var path []string
		// Loop exits when cur is "" (reached a root: the whole path is acyclic);
		// the body has additional break points for settled / missing / cyclic nodes.
		for cur := start; cur != ""; {
			n, ok := nodes[cur]
			if !ok {
				break // manager absent from roster: acyclic tail (orphan, not cycle).
			}
			if colour[cur] == black {
				break // joins an already-settled acyclic chain.
			}
			if colour[cur] == grey {
				// cur is on the current path -> a cycle. Mark every node from cur's
				// position to the end of the path as a cycle member.
				inCycle := false
				for _, p := range path {
					if p == cur {
						inCycle = true
					}
					if inCycle {
						cycle[p] = true
					}
				}
				cycle[cur] = true
				break
			}
			colour[cur] = grey
			path = append(path, cur)
			cur = n.managerID
		}
		// Settle every node we greyed on this path.
		for _, p := range path {
			colour[p] = black
		}
	}
	return cycle
}

// depthOf returns the worker's distance from a tree root by walking the manager
// chain. A chain that hits a missing manager terminates at that point (the
// orphan boundary); a chain that hits a cycle member returns -1 (undefined).
// Bounded by len(nodes): a guard counter stops a pathological chain.
func depthOf(id string, nodes map[string]node, cycleMembers map[string]bool) int {
	depth := 0
	cur := id
	for i := 0; i <= len(nodes); i++ {
		if cycleMembers[cur] {
			return -1
		}
		n, ok := nodes[cur]
		if !ok {
			return depth // walked off a missing manager: depth so far.
		}
		if n.managerID == "" {
			return depth // reached a root.
		}
		depth++
		cur = n.managerID
	}
	return -1 // unreachable given cycle detection, but bounds the walk.
}

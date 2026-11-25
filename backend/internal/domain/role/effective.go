package role

import "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"

// Effective returns the highest-ranked role among the assignments placed on
// nodes of path (the node itself and all its ancestors). Assignments on
// descendants or siblings do not count. ok is false when no assignment applies,
// meaning the node is not visible to the user. The assignments must all belong
// to the same user.
func Effective(path node.Path, assignments []Assignment) (best Role, ok bool) {
	for _, a := range assignments {
		if !path.Contains(a.NodeID) || a.Role.Rank() == 0 {
			continue
		}
		if !ok || a.Role.Rank() > best.Rank() {
			best, ok = a.Role, true
		}
	}
	return best, ok
}

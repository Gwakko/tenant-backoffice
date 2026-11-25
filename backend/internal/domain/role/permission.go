package role

// Permission is a single action a role may allow.
type Permission string

// Permissions, in the order of the authorization matrix.
const (
	NodeRead        Permission = "node.read"
	SettingsRead    Permission = "settings.read"
	BalanceRead     Permission = "balance.read"
	OperationRead   Permission = "operation.read"
	ReportRead      Permission = "report.read"
	AuditRead       Permission = "audit.read"
	MemberRead      Permission = "member.read"
	NodeCreate      Permission = "node.create"
	NodeRename      Permission = "node.rename"
	NodeMove        Permission = "node.move"
	NodeDeactivate  Permission = "node.deactivate"
	SettingsWrite   Permission = "settings.write"
	BalanceCredit   Permission = "balance.credit"
	BalanceDebit    Permission = "balance.debit"
	BalanceTransfer Permission = "balance.transfer"
	MemberManage    Permission = "member.manage"
	NodeDelete      Permission = "node.delete"
)

// AllPermissions lists every permission in matrix order.
func AllPermissions() []Permission {
	return []Permission{
		NodeRead, SettingsRead, BalanceRead, OperationRead, ReportRead, AuditRead,
		MemberRead, NodeCreate, NodeRename, NodeMove, NodeDeactivate, SettingsWrite,
		BalanceCredit, BalanceDebit, BalanceTransfer, MemberManage, NodeDelete,
	}
}

// minRole returns the lowest role that holds p, or "" for an unknown permission.
func minRole(p Permission) Role {
	switch p {
	case NodeRead, SettingsRead, BalanceRead, OperationRead:
		return Viewer
	case ReportRead, AuditRead, MemberRead:
		return Analyst
	case NodeCreate, NodeRename, NodeMove, NodeDeactivate, SettingsWrite,
		BalanceCredit, BalanceDebit, BalanceTransfer, MemberManage:
		return Admin
	case NodeDelete:
		return Owner
	default:
		return ""
	}
}

// Allows reports whether role r holds permission p. Unknown roles or
// permissions are never allowed.
func Allows(r Role, p Permission) bool {
	need := minRole(p).Rank()
	return need > 0 && r.Rank() >= need
}

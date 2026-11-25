package role

// CanGrant reports whether an actor holding actorRole on a node may assign
// target there: the actor needs member.manage and target must not outrank the
// actor, so only an owner can assign owner.
func CanGrant(actorRole, target Role) bool {
	return Allows(actorRole, MemberManage) && target.Rank() > 0 && target.Rank() <= actorRole.Rank()
}

// CanRevoke reports whether an actor holding actorRole on a node may remove an
// assignment of role current there; same ranking rule as CanGrant.
func CanRevoke(actorRole, current Role) bool {
	return CanGrant(actorRole, current)
}

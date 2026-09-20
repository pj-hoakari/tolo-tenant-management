package domain

// EventRole is a role a user holds on one event of the tenant they belong to.
type EventRole struct {
	eventID       string
	eventPublicID string
	role          Role
}

func NewEventRole(eventID, eventPublicID string, role Role) EventRole {
	return EventRole{eventID: eventID, eventPublicID: eventPublicID, role: role}
}

func (r EventRole) EventID() string       { return r.eventID }
func (r EventRole) EventPublicID() string { return r.eventPublicID }
func (r EventRole) Role() Role            { return r.role }

// Membership is a user's membership of one tenant: the tenant role and the
// event roles held inside that tenant. A user may hold memberships of several
// tenants; each is a separate Membership.
type Membership struct {
	userID         string
	tenantID       string
	tenantPublicID string
	tenantRole     Role
	eventRoles     []EventRole
}

func NewMembership(userID, tenantID, tenantPublicID string, tenantRole Role, eventRoles []EventRole) Membership {
	return Membership{
		userID:         userID,
		tenantID:       tenantID,
		tenantPublicID: tenantPublicID,
		tenantRole:     tenantRole,
		eventRoles:     append([]EventRole(nil), eventRoles...),
	}
}

func (m Membership) UserID() string         { return m.userID }
func (m Membership) TenantID() string       { return m.tenantID }
func (m Membership) TenantPublicID() string { return m.tenantPublicID }
func (m Membership) TenantRole() Role       { return m.tenantRole }

// EventRoles returns a copy of the event roles held in the tenant.
func (m Membership) EventRoles() []EventRole { return append([]EventRole(nil), m.eventRoles...) }

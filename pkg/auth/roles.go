package auth

// Role constants for RBAC.
const (
	RoleAdmin    = "admin"    // full access to all endpoints
	RoleOperator = "operator" // read + write (no delete)
	RoleViewer   = "viewer"   // read-only
	RoleTenant   = "tenant"   // tenant-scoped access only
)

// CheckPermission verifies the role has access to the given HTTP method.
//
// Permissions:
//   - Admin: all methods
//   - Operator: GET, POST, PUT, PATCH (no DELETE)
//   - Viewer: GET only
//   - Tenant: GET only (further scoped by tenant ID in middleware)
func CheckPermission(role, method string) bool {
	switch role {
	case RoleAdmin:
		return true
	case RoleOperator:
		switch method {
		case "GET", "POST", "PUT", "PATCH":
			return true
		default:
			return false
		}
	case RoleViewer, RoleTenant:
		return method == "GET"
	default:
		return false
	}
}

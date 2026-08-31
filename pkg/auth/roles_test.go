package auth

import "testing"

func TestRoleConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		want     string
	}{
		{"RoleAdmin", RoleAdmin, "admin"},
		{"RoleOperator", RoleOperator, "operator"},
		{"RoleViewer", RoleViewer, "viewer"},
		{"RoleTenant", RoleTenant, "tenant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.constant, tt.want)
			}
		})
	}
}

func TestCheckPermission_Admin(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	for _, m := range methods {
		if !CheckPermission(RoleAdmin, m) {
			t.Errorf("admin should have access to %s", m)
		}
	}
}

func TestCheckPermission_Operator(t *testing.T) {
	allowed := []string{"GET", "POST", "PUT", "PATCH"}
	for _, m := range allowed {
		if !CheckPermission(RoleOperator, m) {
			t.Errorf("operator should have access to %s", m)
		}
	}
	if CheckPermission(RoleOperator, "DELETE") {
		t.Error("operator should NOT have access to DELETE")
	}
}

func TestCheckPermission_Viewer(t *testing.T) {
	if !CheckPermission(RoleViewer, "GET") {
		t.Error("viewer should have access to GET")
	}
	denied := []string{"POST", "PUT", "PATCH", "DELETE"}
	for _, m := range denied {
		if CheckPermission(RoleViewer, m) {
			t.Errorf("viewer should NOT have access to %s", m)
		}
	}
}

func TestCheckPermission_Tenant(t *testing.T) {
	if !CheckPermission(RoleTenant, "GET") {
		t.Error("tenant should have access to GET")
	}
	denied := []string{"POST", "PUT", "PATCH", "DELETE"}
	for _, m := range denied {
		if CheckPermission(RoleTenant, m) {
			t.Errorf("tenant should NOT have access to %s", m)
		}
	}
}

func TestCheckPermission_UnknownRole(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	for _, m := range methods {
		if CheckPermission("unknown", m) {
			t.Errorf("unknown role should NOT have access to %s", m)
		}
	}
}

func TestCheckPermission_EmptyRole(t *testing.T) {
	if CheckPermission("", "GET") {
		t.Error("empty role should be denied")
	}
}

func TestCheckPermission_FullRBACMatrix(t *testing.T) {
	type testCase struct {
		role   string
		method string
		want   bool
	}

	tests := []testCase{
		// Admin: all allowed
		{RoleAdmin, "GET", true},
		{RoleAdmin, "POST", true},
		{RoleAdmin, "PUT", true},
		{RoleAdmin, "PATCH", true},
		{RoleAdmin, "DELETE", true},
		// Operator: read + write, no delete
		{RoleOperator, "GET", true},
		{RoleOperator, "POST", true},
		{RoleOperator, "PUT", true},
		{RoleOperator, "PATCH", true},
		{RoleOperator, "DELETE", false},
		// Viewer: GET only
		{RoleViewer, "GET", true},
		{RoleViewer, "POST", false},
		{RoleViewer, "PUT", false},
		{RoleViewer, "PATCH", false},
		{RoleViewer, "DELETE", false},
		// Tenant: GET only
		{RoleTenant, "GET", true},
		{RoleTenant, "POST", false},
		{RoleTenant, "PUT", false},
		{RoleTenant, "PATCH", false},
		{RoleTenant, "DELETE", false},
		// Unknown: nothing
		{"unknown", "GET", false},
		{"unknown", "POST", false},
		{"unknown", "DELETE", false},
	}

	for _, tt := range tests {
		name := tt.role + "_" + tt.method
		t.Run(name, func(t *testing.T) {
			got := CheckPermission(tt.role, tt.method)
			if got != tt.want {
				t.Errorf("CheckPermission(%q, %q) = %v, want %v", tt.role, tt.method, got, tt.want)
			}
		})
	}
}

// #1756 — how the /login email/password form names the tenant.
//
// POST /auth/local/login resolves the tenant server side when exactly one
// exists, so the form only needs a tenant when there are several. The
// install-state endpoint says which case this is (`single_tenant`), and
// during first install it also hands over the bootstrap tenant id.
//
//   - "hidden":   send the known tenant id in a hidden field (first install).
//   - "implicit": send no tenant; the server picks the only one.
//   - "ask":      show a tenant ID field. `required` is false when
//                 install-state could not be read: the server may still
//                 resolve a single tenant, so an empty field is allowed.

export type InstallState = {
  first_install?: boolean;
  tenant_id?: string;
  single_tenant?: boolean;
};

export type LoginTenantMode =
  | { kind: "hidden"; tenantID: string }
  | { kind: "implicit" }
  | { kind: "ask"; required: boolean };

export function loginTenantMode(state: InstallState | null): LoginTenantMode {
  if (state === null) return { kind: "ask", required: false };
  if (state.first_install && state.tenant_id) {
    return { kind: "hidden", tenantID: state.tenant_id };
  }
  if (state.single_tenant) return { kind: "implicit" };
  return { kind: "ask", required: true };
}

// localLoginBody builds the JSON body for POST /auth/local/login. An empty
// tenant is left out rather than sent as "", so the server resolves it.
export function localLoginBody(
  tenantID: string,
  email: string,
  password: string,
): { tenant_id?: string; email: string; password: string } {
  return tenantID
    ? { tenant_id: tenantID, email, password }
    : { email, password };
}

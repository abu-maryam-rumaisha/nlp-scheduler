// Typed client for the Go API. The session lives in an HttpOnly cookie set by
// /auth/login, so requests only need same-origin credentials.

export interface Role {
  id: number;
  name: string;
  description: string;
  created_at: string;
}

export interface User {
  id: number;
  username: string;
  role: string;
  created_at: string;
  updated_at: string;
}

export interface APIKey {
  id: number;
  user_id: number;
  owner: string;
  name: string;
  prefix: string;
  role: string;
  expires_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

export interface Instance {
  id: number;
  name: string;
  service: string;
  host: string;
  description: string;
  status: 'online' | 'offline' | 'unknown';
  last_seen_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface InstanceConfig {
  config_name: string;
  template_id: number | null;
  state: 'up' | 'down';
  deployment_id: number;
  updated_at: string;
}

export interface Variable {
  name: string;
  default?: string;
  required: boolean;
  description?: string;
}

export interface Directive {
  id?: number;
  name: string;
  args?: string[];
  block?: boolean;
  raw?: string;
  comment?: string;
  children?: Directive[];
}

export interface Template {
  id: number;
  name: string;
  description: string;
  variables: Variable[];
  directives?: Directive[];
  created_at: string;
  updated_at: string;
}

export type DeploymentStatus = 'pending' | 'published' | 'applied' | 'failed';

export interface Deployment {
  id: number;
  instance_id: number;
  instance: string;
  service: string;
  template_id: number | null;
  config_name: string;
  action: 'up' | 'down';
  variables: Record<string, string>;
  rendered_config?: string;
  status: DeploymentStatus;
  error?: string;
  requested_by: string;
  /** Set on a down issued to remove that deployment. */
  removes_deployment_id?: number;
  created_at: string;
  updated_at: string;
}

export interface Target {
  instances?: string[];
  service?: string;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly field?: string,
  ) {
    super(message);
  }
}

/** Fired on window when the server rejects the session. */
export const SESSION_EXPIRED = 'session-expired';

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    if (res.status === 401 && path !== '/auth/login') {
      window.dispatchEvent(new Event(SESSION_EXPIRED));
    }
    throw new ApiError(res.status, data.error ?? res.statusText, data.field);
  }
  return data as T;
}

const qs = (params: Record<string, string | number | undefined>) => {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') q.set(k, String(v));
  const s = q.toString();
  return s ? `?${s}` : '';
};

export const api = {
  login: (username: string, password: string) =>
    request<{ user: User }>('POST', '/auth/login', { username, password }),
  logout: () => request<void>('POST', '/auth/logout'),
  me: () => request<User>('GET', '/auth/me'),
  changePassword: (current_password: string, new_password: string) =>
    request<{ user: User }>('PUT', '/auth/password', { current_password, new_password }),

  listRoles: () => request<{ roles: Role[] }>('GET', '/roles').then((r) => r.roles),
  listUsers: () => request<{ users: User[] }>('GET', '/users').then((r) => r.users),
  createUser: (u: { username: string; password: string; role: string }) => request<User>('POST', '/users', u),
  updateUser: (id: number, patch: { role?: string; password?: string }) => request<User>('PATCH', `/users/${id}`, patch),
  deleteUser: (id: number) => request<void>('DELETE', `/users/${id}`),

  listAPIKeys: (all = false) =>
    request<{ api_keys: APIKey[] }>('GET', `/api-keys${all ? '?all=true' : ''}`).then((r) => r.api_keys),
  /** Creates a key; the returned `key` is the only time the secret is shown. */
  createAPIKey: (req: { name: string; role: string; expires_in_days?: number }) =>
    request<{ key: string; api_key: APIKey }>('POST', '/api-keys', req),
  deleteAPIKey: (id: number) => request<void>('DELETE', `/api-keys/${id}`),

  listInstances: (service?: string) =>
    request<{ instances: Instance[] }>('GET', `/instances${qs({ service })}`).then((r) => r.instances),
  createInstance: (i: Partial<Instance>) => request<Instance>('POST', '/instances', i),
  updateInstance: (id: number, i: Partial<Instance>) => request<Instance>('PUT', `/instances/${id}`, i),
  deleteInstance: (id: number) => request<void>('DELETE', `/instances/${id}`),
  instanceConfigs: (id: number) =>
    request<{ configs: InstanceConfig[] }>('GET', `/instances/${id}/configs`).then((r) => r.configs),

  listTemplates: () => request<{ templates: Template[] }>('GET', '/templates').then((r) => r.templates),
  getTemplate: (id: number) => request<Template>('GET', `/templates/${id}`),
  createTemplate: (t: unknown) => request<Template>('POST', '/templates', t),
  replaceTemplate: (id: number, t: unknown) => request<Template>('PUT', `/templates/${id}`, t),
  deleteTemplate: (id: number) => request<void>('DELETE', `/templates/${id}`),
  renderTemplate: (id: number, variables: Record<string, string>) =>
    request<{ config: string; variables: Record<string, string> }>('POST', `/templates/${id}/render`, { variables }),

  /** One page of deployments, newest first, and how many match in total. */
  listDeployments: (
    f: { instance_id?: number; status?: string; config_name?: string; limit?: number; offset?: number } = {},
  ) => request<{ deployments: Deployment[]; total: number }>('GET', `/deployments${qs(f)}`),
  getDeployment: (id: number) => request<Deployment>('GET', `/deployments/${id}`),
  /**
   * Removes a deployment. If its config file is live, resolves to the down
   * that removes it (the row goes once that down is applied); otherwise the
   * row is deleted at once and it resolves to null.
   */
  removeDeployment: (id: number) =>
    request<{ deployment: Deployment } | undefined>('DELETE', `/deployments/${id}`).then((r) => r?.deployment ?? null),
  up: (req: { template_id: number; config_name: string; variables: Record<string, string> } & Target) =>
    request<{ deployments: Deployment[] }>('POST', '/deployments/up', req).then((r) => r.deployments),
  down: (req: { config_name: string } & Target) =>
    request<{ deployments: Deployment[] }>('POST', '/deployments/down', req).then((r) => r.deployments),
};

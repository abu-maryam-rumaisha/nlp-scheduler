import { html, nothing } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { api, type APIKey, type Role } from '../api';
import { confirmAction } from '../components/confirm';
import { clampPage, pageOf, pagedTable, type PageState } from '../components/pager';
import { LightElement, errorMessage, notify, session, timeAgo, valueOf } from '../core';

// Mirrors auth.RoleAtMost on the server: a key's role cannot exceed yours.
const RANK: Record<string, number> = { viewer: 1, prosecutor: 2 };

const EXPIRY_OPTIONS = [
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '365', label: '1 year' },
  { value: '0', label: 'Never' },
];

function expiryCell(k: APIKey) {
  if (!k.expires_at) return html`<span class="text-(--wa-color-text-quiet)">never</span>`;
  const at = new Date(k.expires_at);
  if (at.getTime() <= Date.now()) {
    return html`<wa-badge variant="danger" appearance="filled-outlined" pill>expired</wa-badge>`;
  }
  return html`<span title=${at.toLocaleString()}>${at.toLocaleDateString()}</span>`;
}

@customElement('api-keys-page')
export class APIKeysPage extends LightElement {
  @state() private keys: APIKey[] = [];
  @state() private roles: Role[] = [];
  @state() private loading = true;
  @state() private showAll = false;
  @state() private paging: PageState = { page: 1, pageSize: 20 };

  @state() private createOpen = false;
  @state() private formError = '';
  @state() private saving = false;
  /** The just-created key, shown once. */
  @state() private created: { key: string; apiKey: APIKey } | null = null;

  override connectedCallback() {
    super.connectedCallback();
    this.load();
  }

  private async load() {
    try {
      [this.keys, this.roles] = await Promise.all([api.listAPIKeys(this.showAll), api.listRoles()]);
    } catch (e) {
      notify(errorMessage(e), 'danger');
    } finally {
      this.loading = false;
    }
  }

  /** Roles the signed-in user may give a key. */
  private get grantableRoles() {
    const mine = RANK[session.user!.role] ?? 0;
    return this.roles.filter((r) => (RANK[r.name] ?? Infinity) <= mine);
  }

  private async create(e: SubmitEvent) {
    e.preventDefault();
    const form = e.target as HTMLFormElement;
    const days = Number(valueOf(form, '[name=expires]'));
    this.saving = true;
    this.formError = '';
    try {
      const r = await api.createAPIKey({
        name: valueOf(form, '[name=name]'),
        role: valueOf(form, '[name=role]'),
        expires_in_days: days || undefined,
      });
      this.createOpen = false;
      this.created = { key: r.key, apiKey: r.api_key };
      await this.load();
    } catch (err) {
      this.formError = errorMessage(err);
    } finally {
      this.saving = false;
    }
  }

  private async revoke(k: APIKey) {
    const ok = await confirmAction({
      title: 'Revoke API key',
      body: html`Revoke <strong>${k.name}</strong> (<code>${k.prefix}…</code>)${k.owner !== session.user!.username
          ? html` owned by <strong>${k.owner}</strong>`
          : nothing}? Anything using it stops working immediately.`,
      confirmLabel: 'Revoke',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteAPIKey(k.id);
      notify(`API key ${k.name} revoked`, 'success');
      await this.load();
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
  }

  private renderTable() {
    if (this.loading) return html`<div class="grid place-items-center py-16"><wa-spinner class="text-2xl"></wa-spinner></div>`;
    if (this.keys.length === 0) {
      return html`<wa-callout variant="neutral">
        No API keys yet. Create one for scripts and CI to call the API without signing in.
      </wa-callout>`;
    }
    const paging = clampPage(this.paging, this.keys.length);
    const table = html`
      <table class="data-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Key</th>
            <th>Role</th>
            ${this.showAll ? html`<th>Owner</th>` : nothing}
            <th>Created</th>
            <th>Expires</th>
            <th>Last used</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          ${pageOf(this.keys, paging).map(
            (k) => html`
              <tr>
                <td class="font-medium">${k.name}</td>
                <td class="font-mono text-xs text-(--wa-color-text-quiet)">${k.prefix}_…</td>
                <td>
                  <wa-badge variant=${k.role === 'prosecutor' ? 'brand' : 'neutral'} appearance="filled-outlined"
                    >${k.role}</wa-badge
                  >
                </td>
                ${this.showAll ? html`<td>${k.owner}</td>` : nothing}
                <td class="text-(--wa-color-text-quiet)" title=${new Date(k.created_at).toLocaleString()}>
                  ${timeAgo(k.created_at)}
                </td>
                <td>${expiryCell(k)}</td>
                <td class="text-(--wa-color-text-quiet)">${timeAgo(k.last_used_at)}</td>
                <td class="text-right">
                  <wa-button size="s" appearance="plain" variant="danger" title="Revoke" @click=${() => this.revoke(k)}>
                    <wa-icon name="trash" label="Revoke"></wa-icon>
                  </wa-button>
                </td>
              </tr>
            `,
          )}
        </tbody>
      </table>
    `;
    return pagedTable({
      label: 'API keys',
      table,
      total: this.keys.length,
      state: paging,
      onChange: (st) => (this.paging = st),
    });
  }

  private renderCreateDialog() {
    const roles = this.grantableRoles;
    return html`
      <wa-dialog
        label="New API key"
        ?open=${this.createOpen}
        @wa-after-hide=${(e: Event) => e.target === e.currentTarget && (this.createOpen = false)}
      >
        ${this.createOpen
          ? html`<form id="api-key-form" class="flex flex-col gap-4" @submit=${this.create}>
              ${this.formError ? html`<wa-callout variant="danger" size="s">${this.formError}</wa-callout>` : nothing}
              <wa-input name="name" label="Name" required maxlength="100" placeholder="e.g. ci-deploy" hint="What will use this key?"></wa-input>
              <wa-select name="role" label="Role" required value=${roles[0]?.name ?? ''}>
                ${roles.map((r) => html`<wa-option value=${r.name}>${r.name}</wa-option>`)}
              </wa-select>
              <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs text-(--wa-color-text-quiet)">
                ${roles.map((r) => html`<dt class="font-medium">${r.name}</dt><dd>${r.description}</dd>`)}
              </dl>
              <p class="text-xs text-(--wa-color-text-quiet)">
                Whatever the role, a key cannot manage users, API keys or passwords; those need you signed in.
              </p>
              <wa-select name="expires" label="Expires" value="90">
                ${EXPIRY_OPTIONS.map((o) => html`<wa-option value=${o.value}>${o.label}</wa-option>`)}
              </wa-select>
            </form>`
          : nothing}
        <wa-button slot="footer" @click=${() => (this.createOpen = false)}>Cancel</wa-button>
        <wa-button slot="footer" variant="brand" type="submit" form="api-key-form" ?loading=${this.saving}>Create key</wa-button>
      </wa-dialog>
    `;
  }

  private renderCreatedDialog() {
    const c = this.created;
    const example = c ? `curl -H "X-API-Key: ${c.key}" ${location.origin}/api/v1/instances` : '';
    return html`
      <wa-dialog
        label="Copy your API key"
        style="--width: 40rem"
        ?open=${!!c}
        @wa-after-hide=${(e: Event) => e.target === e.currentTarget && (this.created = null)}
      >
        ${c
          ? html`<div class="flex flex-col gap-4 text-sm">
              <wa-callout variant="warning" size="s">
                This is the only time the key is shown. Store it somewhere safe; if you lose it, revoke it and create a new one.
              </wa-callout>
              <div>
                <div class="mb-1 font-medium">${c.apiKey.name} · ${c.apiKey.role}</div>
                <div class="flex items-center gap-2 rounded-lg bg-(--wa-color-surface-lowered) px-3 py-2">
                  <code class="min-w-0 flex-1 text-xs break-all" data-testid="new-key">${c.key}</code>
                  <wa-copy-button value=${c.key}></wa-copy-button>
                </div>
              </div>
              <div>
                <div class="mb-1 font-medium">Use it as a header</div>
                <div class="flex items-start gap-2 rounded-lg bg-(--wa-color-surface-lowered) px-3 py-2">
                  <code class="min-w-0 flex-1 text-xs break-all">${example}</code>
                  <wa-copy-button value=${example}></wa-copy-button>
                </div>
                <p class="mt-2 text-xs text-(--wa-color-text-quiet)">
                  <code>Authorization: Bearer &lt;key&gt;</code> works too. Keys cannot manage users, API keys or passwords.
                </p>
              </div>
            </div>`
          : nothing}
        <wa-button slot="footer" variant="brand" @click=${() => (this.created = null)}>Done</wa-button>
      </wa-dialog>
    `;
  }

  override render() {
    return html`
      <div class="mb-6 flex flex-wrap items-center gap-3">
        <h1 class="mr-auto text-2xl font-semibold">API keys</h1>
        ${session.canWrite
          ? html`<wa-switch
              size="s"
              ?checked=${this.showAll}
              @change=${(e: Event) => {
                this.showAll = (e.target as HTMLInputElement).checked;
                this.paging = { ...this.paging, page: 1 };
                this.load();
              }}
              >All users</wa-switch
            >`
          : nothing}
        <wa-button
          size="s"
          variant="brand"
          @click=${() => {
            this.formError = '';
            this.createOpen = true;
          }}
        >
          <wa-icon slot="start" name="key-plus"></wa-icon>New API key
        </wa-button>
      </div>
      ${this.renderTable()} ${this.renderCreateDialog()} ${this.renderCreatedDialog()}
    `;
  }
}

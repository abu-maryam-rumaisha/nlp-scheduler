import { html, nothing } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { api, type Deployment, type Instance, type InstanceConfig } from '../api';
import { confirmAction } from '../components/confirm';
import '../components/deploy-dialog';
import { clampPage, pageOf, pagedTable, type PageState } from '../components/pager';
import { statusBadge } from '../components/status';
import { LightElement, errorMessage, notify, session, timeAgo, valueOf } from '../core';

const instanceBadge = (status: Instance['status']) => {
  const variant = { online: 'success', offline: 'danger', unknown: 'neutral' }[status];
  return html`<wa-badge variant=${variant} appearance="filled-outlined" pill>${status}</wa-badge>`;
};

@customElement('instances-page')
export class InstancesPage extends LightElement {
  @state() private instances: Instance[] = [];
  @state() private loading = true;
  @state() private filter = '';
  @state() private paging: PageState = { page: 1, pageSize: 20 };

  // Details drawer.
  @state() private selected: Instance | null = null;
  @state() private configs: InstanceConfig[] = [];
  @state() private recent: Deployment[] = [];

  // Create/edit dialog; editing === null with formOpen means create.
  @state() private formOpen = false;
  @state() private editing: Instance | null = null;
  @state() private formError = '';
  @state() private saving = false;

  @state() private deploy: { mode: 'up' | 'down'; preset: string[]; config: string } | null = null;

  private timer?: number;

  override connectedCallback() {
    super.connectedCallback();
    this.load();
    this.timer = window.setInterval(() => this.load(true), 10_000);
  }

  override disconnectedCallback() {
    super.disconnectedCallback();
    clearInterval(this.timer);
  }

  private async load(quiet = false) {
    try {
      this.instances = await api.listInstances();
      if (this.selected) {
        this.selected = this.instances.find((i) => i.id === this.selected!.id) ?? null;
        if (this.selected) await this.loadDetails(this.selected);
      }
    } catch (e) {
      if (!quiet) notify(errorMessage(e), 'danger');
    } finally {
      this.loading = false;
    }
  }

  private async loadDetails(i: Instance) {
    const [configs, recent] = await Promise.all([
      api.instanceConfigs(i.id),
      api.listDeployments({ instance_id: i.id, limit: 10 }).then((r) => r.deployments),
    ]);
    this.configs = configs;
    this.recent = recent;
  }

  private async openDetails(i: Instance) {
    this.selected = i;
    this.configs = [];
    this.recent = [];
    try {
      await this.loadDetails(i);
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
  }

  private openForm(i: Instance | null) {
    this.editing = i;
    this.formError = '';
    this.formOpen = true;
  }

  private async save(e: SubmitEvent) {
    e.preventDefault();
    const form = e.target as HTMLFormElement;
    const body = {
      name: valueOf(form, '[name=name]'),
      service: valueOf(form, '[name=service]'),
      host: valueOf(form, '[name=host]'),
      description: valueOf(form, '[name=description]'),
    };
    this.saving = true;
    try {
      if (this.editing) await api.updateInstance(this.editing.id, body);
      else await api.createInstance(body);
      notify(`Instance ${body.name} saved`, 'success');
      this.formOpen = false;
      await this.load();
    } catch (err) {
      this.formError = errorMessage(err);
    } finally {
      this.saving = false;
    }
  }

  private async confirmDelete(i: Instance) {
    const ok = await confirmAction({
      title: 'Delete instance',
      body: html`Delete <strong>${i.name}</strong> and its deployment history? A running agent will register it again on its
        next heartbeat.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteInstance(i.id);
      if (this.selected?.id === i.id) this.selected = null;
      notify(`Instance ${i.name} deleted`, 'success');
      await this.load();
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
  }

  private get filtered() {
    const f = this.filter.trim().toLowerCase();
    if (!f) return this.instances;
    return this.instances.filter((i) => [i.name, i.service, i.host].some((s) => s.toLowerCase().includes(f)));
  }

  private renderTable() {
    if (this.loading) return html`<div class="grid place-items-center py-16"><wa-spinner class="text-2xl"></wa-spinner></div>`;
    if (this.instances.length === 0) {
      return html`<wa-callout variant="neutral">
        No instances yet. Start an agent with <code>AGENT_INSTANCE</code> and <code>AGENT_SERVICE</code> set and it will
        register itself${session.canWrite ? ', or register one by hand' : ''}.
      </wa-callout>`;
    }
    const counts = {
      online: this.instances.filter((i) => i.status === 'online').length,
      total: this.instances.length,
    };
    const filtered = this.filtered;
    const paging = clampPage(this.paging, filtered.length);
    const table = html`
        <table class="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Service</th>
              <th>Host</th>
              <th>Status</th>
              <th>Last seen</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            ${pageOf(filtered, paging).map(
              (i) => html`
                <tr
                  class="cursor-pointer"
                  @click=${() => this.openDetails(i)}
                >
                  <td class="font-medium whitespace-nowrap">${i.name}</td>
                  <td><wa-badge variant="neutral" appearance="outlined">${i.service}</wa-badge></td>
                  <td class="font-mono text-xs text-(--wa-color-text-quiet)">${i.host || '—'}</td>
                  <td>${instanceBadge(i.status)}</td>
                  <td class="text-(--wa-color-text-quiet)">${timeAgo(i.last_seen_at)}</td>
                  <td class="text-right whitespace-nowrap" @click=${(e: Event) => e.stopPropagation()}>
                    ${session.canWrite
                      ? html`
                          <wa-button size="s" appearance="plain" title="Deploy" @click=${() => (this.deploy = { mode: 'up', preset: [i.name], config: '' })}>
                            <wa-icon name="up" label="Deploy"></wa-icon>
                          </wa-button>
                          <wa-button size="s" appearance="plain" title="Edit" @click=${() => this.openForm(i)}>
                            <wa-icon name="edit" label="Edit"></wa-icon>
                          </wa-button>
                          <wa-button size="s" appearance="plain" variant="danger" title="Delete" @click=${() => this.confirmDelete(i)}>
                            <wa-icon name="trash" label="Delete"></wa-icon>
                          </wa-button>
                        `
                      : html`<wa-button size="s" appearance="plain" @click=${() => this.openDetails(i)}>View</wa-button>`}
                  </td>
                </tr>
              `,
            )}
          </tbody>
        </table>
    `;
    return html`
      <p class="mb-3 text-sm text-(--wa-color-text-quiet)">${counts.online} of ${counts.total} online</p>
      ${filtered.length === 0
        ? html`<wa-callout variant="neutral">No instances match “${this.filter}”.</wa-callout>`
        : pagedTable({
            label: 'Instances',
            table,
            total: filtered.length,
            state: paging,
            onChange: (st) => (this.paging = st),
          })}
    `;
  }

  private renderDrawer() {
    const i = this.selected;
    return html`
      <wa-drawer
        label=${i?.name ?? ''}
        style="--size: min(36rem, 100vw)"
        ?open=${!!i}
        @wa-after-hide=${(e: Event) => e.target === e.currentTarget && (this.selected = null)}
      >
        ${i
          ? html`
              <div class="flex flex-col gap-6">
                <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
                  <dt class="text-(--wa-color-text-quiet)">Status</dt>
                  <dd>${instanceBadge(i.status)}</dd>
                  <dt class="text-(--wa-color-text-quiet)">Service</dt>
                  <dd>${i.service}</dd>
                  <dt class="text-(--wa-color-text-quiet)">Host</dt>
                  <dd class="font-mono text-xs">${i.host || '—'}</dd>
                  <dt class="text-(--wa-color-text-quiet)">Last seen</dt>
                  <dd>${timeAgo(i.last_seen_at)}</dd>
                  ${i.description
                    ? html`<dt class="text-(--wa-color-text-quiet)">Description</dt>
                        <dd>${i.description}</dd>`
                    : nothing}
                </dl>

                <section>
                  <h3 class="mb-2 font-semibold">Configs</h3>
                  ${this.configs.length === 0
                    ? html`<p class="text-sm text-(--wa-color-text-quiet)">No configs applied yet.</p>`
                    : html`<ul class="divide-y divide-(--wa-color-surface-border) rounded-lg border border-(--wa-color-surface-border)">
                        ${this.configs.map(
                          (c) => html`<li class="flex items-center gap-3 px-3 py-2 text-sm">
                            <span class="font-mono">${c.config_name}</span>
                            ${c.state === 'up'
                              ? html`<span class="font-mono text-xs text-(--wa-color-text-quiet)">${c.deployment_id}.conf</span>`
                              : nothing}
                            <wa-badge variant=${c.state === 'up' ? 'success' : 'neutral'} appearance="filled-outlined">${c.state}</wa-badge>
                            <span class="ml-auto text-xs text-(--wa-color-text-quiet)">${timeAgo(c.updated_at)}</span>
                            ${session.canWrite && c.state === 'up'
                              ? html`<wa-button
                                  size="s"
                                  appearance="outlined"
                                  variant="danger"
                                  @click=${() => (this.deploy = { mode: 'down', preset: [i.name], config: c.config_name })}
                                  >Take down</wa-button
                                >`
                              : nothing}
                          </li>`,
                        )}
                      </ul>`}
                </section>

                <section>
                  <h3 class="mb-2 font-semibold">Recent deployments</h3>
                  ${this.recent.length === 0
                    ? html`<p class="text-sm text-(--wa-color-text-quiet)">None.</p>`
                    : html`<ul class="flex flex-col gap-2">
                        ${this.recent.map(
                          (d) => html`<li class="rounded-lg border border-(--wa-color-surface-border) px-3 py-2 text-sm">
                            <div class="flex items-center gap-2">
                              <span class="font-mono">#${d.id}</span>
                              <span class="font-medium">${d.action}</span>
                              <span class="font-mono">${d.config_name}</span>
                              <span class="ml-auto">${statusBadge(d.status)}</span>
                            </div>
                            <div class="mt-1 text-xs text-(--wa-color-text-quiet)">
                              ${timeAgo(d.created_at)}${d.requested_by ? ` by ${d.requested_by}` : ''}
                            </div>
                            ${d.error ? html`<pre class="mt-2 text-xs whitespace-pre-wrap text-(--wa-color-danger-on-quiet)">${d.error}</pre>` : nothing}
                          </li>`,
                        )}
                      </ul>`}
                </section>
              </div>
              ${session.canWrite
                ? html`<wa-button slot="footer" variant="brand" @click=${() => (this.deploy = { mode: 'up', preset: [i.name], config: '' })}>
                    <wa-icon slot="start" name="up"></wa-icon>Deploy config
                  </wa-button>`
                : nothing}
            `
          : nothing}
      </wa-drawer>
    `;
  }

  private renderForm() {
    const i = this.editing;
    return html`
      <wa-dialog
        label=${i ? `Edit ${i.name}` : 'Register instance'}
        ?open=${this.formOpen}
        @wa-after-hide=${(e: Event) => e.target === e.currentTarget && (this.formOpen = false)}
      >
        ${this.formOpen
          ? html`<form id="instance-form" class="flex flex-col gap-4" @submit=${this.save}>
              ${this.formError ? html`<wa-callout variant="danger" size="s">${this.formError}</wa-callout>` : nothing}
              <wa-input name="name" label="Name" required value=${i?.name ?? ''} hint="Must match the agent's AGENT_INSTANCE"></wa-input>
              <wa-input name="service" label="Service" required value=${i?.service ?? ''} hint="Must match the agent's AGENT_SERVICE"></wa-input>
              <wa-input name="host" label="Host" value=${i?.host ?? ''}></wa-input>
              <wa-textarea name="description" label="Description" rows="2" value=${i?.description ?? ''}></wa-textarea>
            </form>`
          : nothing}
        <wa-button slot="footer" @click=${() => (this.formOpen = false)}>Cancel</wa-button>
        <wa-button slot="footer" variant="brand" type="submit" form="instance-form" ?loading=${this.saving}>Save</wa-button>
      </wa-dialog>
    `;
  }

  override render() {
    return html`
      <div class="mb-6 flex flex-wrap items-center gap-3">
        <h1 class="mr-auto text-2xl font-semibold">Instances</h1>
        <wa-input
          size="s"
          placeholder="Filter by name, service or host"
          with-clear
          class="w-64"
          @input=${(e: Event) => {
            this.filter = (e.target as HTMLInputElement).value;
            this.paging = { ...this.paging, page: 1 };
          }}
        ></wa-input>
        <wa-button size="s" appearance="outlined" @click=${() => this.load()} title="Refresh">
          <wa-icon name="refresh" label="Refresh"></wa-icon>
        </wa-button>
        ${session.canWrite
          ? html`
              <wa-button size="s" appearance="outlined" variant="danger" @click=${() => (this.deploy = { mode: 'down', preset: [], config: '' })}>
                <wa-icon slot="start" name="down"></wa-icon>Take down
              </wa-button>
              <wa-button size="s" appearance="outlined" @click=${() => (this.deploy = { mode: 'up', preset: [], config: '' })}>
                <wa-icon slot="start" name="up"></wa-icon>Deploy
              </wa-button>
              <wa-button size="s" variant="brand" @click=${() => this.openForm(null)}>
                <wa-icon slot="start" name="plus"></wa-icon>Register
              </wa-button>
            `
          : nothing}
      </div>
      ${this.renderTable()} ${this.renderDrawer()} ${this.renderForm()}
      <deploy-dialog
        ?open=${!!this.deploy}
        mode=${this.deploy?.mode ?? 'up'}
        presetConfig=${this.deploy?.config ?? ''}
        .instances=${this.instances}
        .preset=${this.deploy?.preset ?? []}
        @closed=${() => (this.deploy = null)}
        @deployed=${() => setTimeout(() => this.load(true), 1500)}
      ></deploy-dialog>
    `;
  }
}

import { html, nothing } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { api, type Deployment, type Instance } from '../api';
import { confirmAction } from '../components/confirm';
import '../components/deploy-dialog';
import { pagedTable, type PageState } from '../components/pager';
import { statusBadge } from '../components/status';
import { LightElement, errorMessage, notify, session, timeAgo } from '../core';

interface DeployRequest {
  mode: 'up' | 'down';
  instances: string[];
  config: string;
  template: number | null;
  values: Record<string, string>;
}

@customElement('deployments-page')
export class DeploymentsPage extends LightElement {
  @state() private deployments: Deployment[] = [];
  @state() private total = 0;
  @state() private paging: PageState = { page: 1, pageSize: 20 };
  @state() private instances: Instance[] = [];
  @state() private deploy: DeployRequest | null = null;
  @state() private loading = true;
  @state() private status = '';
  @state() private configName = '';
  @state() private detail: Deployment | null = null;

  private timer?: number;

  override connectedCallback() {
    super.connectedCallback();
    this.load();
    // Poll while something is still in flight.
    this.timer = window.setInterval(() => {
      if (this.deployments.some((d) => d.status === 'pending' || d.status === 'published')) this.load(true);
    }, 3000);
  }

  override disconnectedCallback() {
    super.disconnectedCallback();
    clearInterval(this.timer);
  }

  private async load(quiet = false): Promise<void> {
    try {
      const { page, pageSize } = this.paging;
      const r = await api.listDeployments({
        status: this.status,
        config_name: this.configName,
        limit: pageSize,
        offset: (page - 1) * pageSize,
      });
      this.deployments = r.deployments;
      this.total = r.total;
      // The page can fall off the end when the filter narrows the results.
      const last = Math.max(1, Math.ceil(r.total / pageSize));
      if (page > last) {
        this.paging = { ...this.paging, page: last };
        return this.load(quiet);
      }
    } catch (e) {
      if (!quiet) notify(errorMessage(e), 'danger');
    } finally {
      this.loading = false;
    }
  }

  private async open(d: Deployment) {
    try {
      this.detail = await api.getDeployment(d.id);
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
  }

  /** Opens the deploy dialog; instances are loaded fresh for the target picker. */
  private async openDeploy(req: DeployRequest) {
    try {
      this.instances = await api.listInstances();
    } catch (e) {
      notify(errorMessage(e), 'danger');
      return;
    }
    this.detail = null;
    this.deploy = req;
  }

  private redeploy(d: Deployment) {
    return this.openDeploy({
      mode: 'up',
      instances: [d.instance],
      config: d.config_name,
      template: d.template_id,
      values: d.variables,
    });
  }

  private takeDown(d: Deployment) {
    return this.openDeploy({ mode: 'down', instances: [d.instance], config: d.config_name, template: null, values: {} });
  }

  private async removeDeployment(d: Deployment) {
    const ok = await confirmAction({
      title: 'Remove deployment',
      body: html`Remove deployment <strong>#${d.id}</strong> from <strong>${d.instance}</strong>? If
        <span class="font-mono">${d.id}.conf</span> is live, the agent deletes it, runs the config test and reloads
        OpenResty before the deployment is removed.`,
      confirmLabel: 'Remove',
      danger: true,
    });
    if (!ok) return;
    try {
      const down = await api.removeDeployment(d.id);
      notify(
        down ? `Removing ${d.id}.conf from ${d.instance} (deployment #${down.id})` : `Deployment #${d.id} removed`,
        'success',
      );
      this.detail = null;
      await this.load();
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
  }

  private renderDetail() {
    const d = this.detail;
    return html`
      <wa-dialog
        label=${d ? `Deployment #${d.id}` : ''}
        style="--width: 48rem"
        ?open=${!!d}
        @wa-after-hide=${(e: Event) => e.target === e.currentTarget && (this.detail = null)}
      >
        ${d
          ? html`
              <div class="flex flex-col gap-5 text-sm">
                <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2">
                  <dt class="text-(--wa-color-text-quiet)">Status</dt>
                  <dd>${statusBadge(d.status)}</dd>
                  <dt class="text-(--wa-color-text-quiet)">Action</dt>
                  <dd>
                    ${d.action} <span class="font-mono">${d.config_name}</span>
                    ${d.action === 'up' ? html`<span class="text-(--wa-color-text-quiet)">· ${d.id}.conf</span>` : nothing}
                    ${d.removes_deployment_id
                      ? html`<span class="text-(--wa-color-text-quiet)">· removes #${d.removes_deployment_id}</span>`
                      : nothing}
                  </dd>
                  <dt class="text-(--wa-color-text-quiet)">Instance</dt>
                  <dd>${d.instance} <span class="text-(--wa-color-text-quiet)">· ${d.service}</span></dd>
                  <dt class="text-(--wa-color-text-quiet)">Requested</dt>
                  <dd>${new Date(d.created_at).toLocaleString()}${d.requested_by ? ` by ${d.requested_by}` : ''}</dd>
                  <dt class="text-(--wa-color-text-quiet)">Updated</dt>
                  <dd>${new Date(d.updated_at).toLocaleString()}</dd>
                </dl>
                ${d.error
                  ? html`<wa-callout variant="danger"><pre class="text-xs whitespace-pre-wrap">${d.error}</pre></wa-callout>`
                  : nothing}
                ${Object.keys(d.variables).length
                  ? html`<section>
                      <h3 class="mb-2 font-semibold">Variables</h3>
                      <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 font-mono text-xs">
                        ${Object.entries(d.variables).map(([k, v]) => html`<dt class="text-(--wa-color-text-quiet)">${k}</dt><dd>${v}</dd>`)}
                      </dl>
                    </section>`
                  : nothing}
                ${d.rendered_config
                  ? html`<section>
                      <div class="mb-2 flex items-center">
                        <h3 class="font-semibold">Rendered config</h3>
                        <wa-copy-button class="ml-auto" value=${d.rendered_config}></wa-copy-button>
                      </div>
                      <pre class="max-h-96 overflow-auto rounded-lg bg-(--wa-color-surface-lowered) p-3 text-xs">${d.rendered_config}</pre>
                    </section>`
                  : nothing}
              </div>
              ${session.canWrite && d.status !== 'pending' && d.status !== 'published'
                ? html`
                    <wa-button slot="footer" appearance="plain" variant="danger" @click=${() => this.removeDeployment(d)}>
                      <wa-icon slot="start" name="trash"></wa-icon>Remove
                    </wa-button>
                  `
                : nothing}
              ${session.canWrite && d.action === 'up'
                ? html`
                    <wa-button slot="footer" appearance="outlined" variant="danger" @click=${() => this.takeDown(d)}>
                      <wa-icon slot="start" name="down"></wa-icon>Take down
                    </wa-button>
                    <wa-button
                      slot="footer"
                      variant="brand"
                      ?disabled=${d.template_id === null}
                      title=${d.template_id === null ? 'The template was deleted' : 'Deploy again with these settings'}
                      @click=${() => this.redeploy(d)}
                    >
                      <wa-icon slot="start" name="refresh"></wa-icon>Redeploy
                    </wa-button>
                  `
                : nothing}
            `
          : nothing}
      </wa-dialog>
    `;
  }

  override render() {
    return html`
      <div class="mb-6 flex flex-wrap items-center gap-3">
        <h1 class="mr-auto text-2xl font-semibold">Deployments</h1>
        <wa-input
          size="s"
          placeholder="Config name"
          with-clear
          class="w-48"
          @change=${(e: Event) => {
            this.configName = (e.target as HTMLInputElement).value;
            this.paging = { ...this.paging, page: 1 };
            this.load();
          }}
        ></wa-input>
        <wa-select
          size="s"
          placeholder="Any status"
          with-clear
          class="w-40"
          @change=${(e: Event) => {
            this.status = (e.target as HTMLInputElement).value;
            this.paging = { ...this.paging, page: 1 };
            this.load();
          }}
        >
          ${['pending', 'published', 'applied', 'failed'].map((s) => html`<wa-option value=${s}>${s}</wa-option>`)}
        </wa-select>
        <wa-button size="s" appearance="outlined" @click=${() => this.load()} title="Refresh">
          <wa-icon name="refresh" label="Refresh"></wa-icon>
        </wa-button>
        ${session.canWrite
          ? html`
              <wa-button
                size="s"
                appearance="outlined"
                variant="danger"
                @click=${() => this.openDeploy({ mode: 'down', instances: [], config: '', template: null, values: {} })}
              >
                <wa-icon slot="start" name="down"></wa-icon>Take down
              </wa-button>
              <wa-button
                size="s"
                variant="brand"
                @click=${() => this.openDeploy({ mode: 'up', instances: [], config: '', template: null, values: {} })}
              >
                <wa-icon slot="start" name="up"></wa-icon>New deployment
              </wa-button>
            `
          : nothing}
      </div>

      ${this.loading
        ? html`<div class="grid place-items-center py-16"><wa-spinner class="text-2xl"></wa-spinner></div>`
        : this.deployments.length === 0
          ? html`<wa-callout variant="neutral">No deployments match.</wa-callout>`
          : pagedTable({
              label: 'Deployments',
              total: this.total,
              state: this.paging,
              onChange: (st) => {
                this.paging = st;
                this.load();
              },
              table: html`
                <table class="data-table">
                  <thead>
                    <tr>
                      <th>#</th>
                      <th>When</th>
                      <th>Action</th>
                      <th>Config</th>
                      <th>Instance</th>
                      <th>Status</th>
                      <th>By</th>
                    </tr>
                  </thead>
                  <tbody>
                    ${this.deployments.map(
                      (d) => html`
                        <tr
                          class="cursor-pointer"
                          @click=${() => this.open(d)}
                        >
                          <td class="font-mono text-xs">${d.id}</td>
                          <td class="whitespace-nowrap text-(--wa-color-text-quiet)" title=${new Date(d.created_at).toLocaleString()}>
                            ${timeAgo(d.created_at)}
                          </td>
                          <td>
                            <wa-badge variant=${d.action === 'up' ? 'brand' : 'warning'} appearance="outlined">${d.action}</wa-badge>
                          </td>
                          <td class="font-mono">${d.config_name}</td>
                          <td>${d.instance}</td>
                          <td>${statusBadge(d.status)}</td>
                          <td class="text-(--wa-color-text-quiet)">${d.requested_by || '—'}</td>
                        </tr>
                      `,
                    )}
                  </tbody>
                </table>
              `,
            })}
      ${this.renderDetail()}
      <deploy-dialog
        ?open=${!!this.deploy}
        mode=${this.deploy?.mode ?? 'up'}
        presetConfig=${this.deploy?.config ?? ''}
        .instances=${this.instances}
        .preset=${this.deploy?.instances ?? []}
        .presetTemplate=${this.deploy?.template ?? null}
        .presetValues=${this.deploy?.values ?? {}}
        @closed=${() => (this.deploy = null)}
        @deployed=${() => {
          // Show the new rows; polling then follows them to applied/failed.
          this.paging = { ...this.paging, page: 1 };
          this.load();
        }}
      ></deploy-dialog>
    `;
  }
}

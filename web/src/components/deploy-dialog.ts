import { html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { api, type Directive, type Instance, type Target, type Template } from '../api';
import { LightElement, errorMessage, notify } from '../core';

const placeholderRe = /\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g;

/** Placeholder names used anywhere in a directive tree. */
export function placeholders(ds: Directive[] = []): string[] {
  const names = new Set<string>();
  const walk = (list: Directive[]) => {
    for (const d of list) {
      for (const s of [...(d.args ?? []), d.raw ?? '']) for (const m of s.matchAll(placeholderRe)) names.add(m[1]);
      walk(d.children ?? []);
    }
  };
  walk(ds);
  return [...names].sort();
}

/**
 * Brings a config up (render a template with variables) or down on a set of
 * instances or a whole service. Emits `closed`, and `deployed` on success.
 */
@customElement('deploy-dialog')
export class DeployDialog extends LightElement {
  @property({ type: Boolean }) open = false;
  @property() mode: 'up' | 'down' = 'up';
  @property({ attribute: false }) instances: Instance[] = [];
  /** Pre-selected instance names. */
  @property({ attribute: false }) preset: string[] = [];
  @property() presetConfig = '';
  /** Template to select on open (up only), e.g. when redeploying. */
  @property({ attribute: false }) presetTemplate: number | null = null;
  /** Variable values to fill in over the template's defaults. */
  @property({ attribute: false }) presetValues: Record<string, string> = {};

  @state() private templates: Template[] = [];
  @state() private template: Template | null = null;
  @state() private targetKind: 'instances' | 'service' = 'instances';
  @state() private selected: string[] = [];
  @state() private service = '';
  @state() private configName = '';
  @state() private values: Record<string, string> = {};
  @state() private preview = '';
  @state() private busy = false;
  @state() private error = '';
  @state() private errorField = '';

  protected override willUpdate(changed: Map<string, unknown>) {
    if (changed.has('open') && this.open) this.reset();
  }

  private async reset() {
    this.targetKind = 'instances';
    this.selected = [...this.preset];
    this.service = this.instances[0]?.service ?? '';
    this.configName = this.presetConfig;
    this.template = null;
    this.values = {};
    this.preview = '';
    this.error = '';
    if (this.mode === 'up') {
      try {
        this.templates = await api.listTemplates();
      } catch (e) {
        this.error = errorMessage(e);
        return;
      }
      if (this.presetTemplate !== null) {
        if (!this.templates.some((t) => t.id === this.presetTemplate)) {
          this.error = 'The template of that deployment no longer exists; pick another.';
          return;
        }
        await this.pickTemplate(String(this.presetTemplate));
        this.values = { ...this.values, ...this.presetValues };
      }
    }
  }

  private get services() {
    return [...new Set(this.instances.map((i) => i.service))].sort();
  }

  private get variableNames() {
    if (!this.template) return [];
    const declared = this.template.variables.map((v) => v.name);
    return [...new Set([...declared, ...placeholders(this.template.directives)])];
  }

  private async pickTemplate(id: string) {
    this.preview = '';
    if (!id) {
      this.template = null;
      return;
    }
    try {
      this.template = await api.getTemplate(Number(id));
      const values: Record<string, string> = {};
      for (const v of this.template.variables) if (v.default !== undefined) values[v.name] = v.default;
      this.values = values;
      if (!this.configName) this.configName = this.template.name;
    } catch (e) {
      this.error = errorMessage(e);
    }
  }

  private target(): Target {
    return this.targetKind === 'service' ? { service: this.service } : { instances: this.selected };
  }

  /** Only send values the user filled, so declared defaults still apply. */
  private filledValues() {
    return Object.fromEntries(Object.entries(this.values).filter(([, v]) => v !== ''));
  }

  private async renderPreview() {
    if (!this.template) return;
    this.error = '';
    try {
      this.preview = (await api.renderTemplate(this.template.id, this.filledValues())).config;
    } catch (e) {
      this.setError(e);
    }
  }

  private setError(e: unknown) {
    this.error = errorMessage(e);
    this.errorField = (e as { field?: string }).field ?? '';
  }

  private async submit(e: SubmitEvent) {
    e.preventDefault();
    this.error = '';
    if (this.targetKind === 'instances' && this.selected.length === 0) {
      this.error = 'Pick at least one instance.';
      return;
    }
    this.busy = true;
    try {
      const ds =
        this.mode === 'up'
          ? await api.up({
              template_id: this.template!.id,
              config_name: this.configName,
              variables: this.filledValues(),
              ...this.target(),
            })
          : await api.down({ config_name: this.configName, ...this.target() });
      const failed = ds.filter((d) => d.status === 'failed').length;
      notify(
        `${ds.length} ${this.mode} command${ds.length === 1 ? '' : 's'} queued` + (failed ? `, ${failed} failed to publish` : ''),
        failed ? 'warning' : 'success',
      );
      this.dispatchEvent(new Event('deployed'));
      this.close();
    } catch (err) {
      this.setError(err);
    } finally {
      this.busy = false;
    }
  }

  private close() {
    this.dispatchEvent(new Event('closed'));
  }

  private renderTarget() {
    return html`
      <wa-radio-group
        label="Target"
        orientation="horizontal"
        .value=${this.targetKind}
        @change=${(e: Event) => (this.targetKind = (e.target as HTMLInputElement).value as 'instances' | 'service')}
      >
        <wa-radio value="instances">Instances</wa-radio>
        <wa-radio value="service">Whole service</wa-radio>
      </wa-radio-group>
      ${this.targetKind === 'instances'
        ? html`<wa-select
            label="Instances"
            multiple
            with-clear
            max-options-visible="5"
            .value=${this.selected}
            @change=${(e: Event) => (this.selected = [...((e.target as HTMLInputElement).value as unknown as string[])])}
          >
            ${this.instances.map(
              (i) => html`<wa-option value=${i.name}>${i.name}</wa-option>`,
            )}
          </wa-select>`
        : html`<wa-select
            label="Service"
            hint="Every instance currently registered in the service"
            .value=${this.service}
            @change=${(e: Event) => (this.service = (e.target as HTMLInputElement).value)}
          >
            ${this.services.map((s) => html`<wa-option value=${s}>${s}</wa-option>`)}
          </wa-select>`}
    `;
  }

  private renderVariables() {
    if (!this.template) return nothing;
    const declared = new Map(this.template.variables.map((v) => [v.name, v]));
    const names = this.variableNames;
    if (names.length === 0) {
      return html`<p class="text-sm text-(--wa-color-text-quiet)">This template has no variables.</p>`;
    }
    return html`
      <fieldset class="flex flex-col gap-3 rounded-lg border border-(--wa-color-surface-border) p-4">
        <legend class="px-1 text-sm font-medium">Variables</legend>
        ${names.map((name) => {
          const v = declared.get(name);
          const required = v ? v.required && v.default === undefined : true;
          return html`<wa-input
            label=${name}
            size="s"
            placeholder=${v?.default ?? ''}
            hint=${v?.description || (v?.default !== undefined ? `Default: ${v.default}` : '')}
            ?required=${required}
            .value=${this.values[name] ?? ''}
            @input=${(e: Event) => (this.values = { ...this.values, [name]: (e.target as HTMLInputElement).value })}
          ></wa-input>`;
        })}
      </fieldset>
    `;
  }

  override render() {
    const up = this.mode === 'up';
    return html`
      <wa-dialog
        label=${up ? 'Bring config up' : 'Take config down'}
        style="--width: 40rem"
        ?open=${this.open}
        @wa-after-hide=${(e: Event) => e.target === e.currentTarget && this.close()}
      >
        <form id="deploy-form" class="flex flex-col gap-4" @submit=${this.submit}>
          ${this.error
            ? html`<wa-callout variant="danger" size="s"
                >${this.errorField ? html`<strong>${this.errorField}:</strong> ` : ''}${this.error}</wa-callout
              >`
            : ''}
          ${up
            ? html`<wa-select
                label="Template"
                required
                .value=${this.template ? String(this.template.id) : ''}
                @change=${(e: Event) => this.pickTemplate((e.target as HTMLInputElement).value)}
              >
                ${this.templates.map((t) => html`<wa-option value=${String(t.id)}>${t.name}</wa-option>`)}
              </wa-select>`
            : ''}
          <wa-input
            label="Config name"
            hint="Written to conf.d/<name>.conf on each instance"
            required
            pattern="[A-Za-z0-9][A-Za-z0-9_\\-]{0,127}"
            .value=${this.configName}
            @input=${(e: Event) => (this.configName = (e.target as HTMLInputElement).value)}
          ></wa-input>
          ${this.renderTarget()} ${up ? this.renderVariables() : ''}
          ${this.preview
            ? html`<pre class="max-h-72 overflow-auto rounded-lg bg-(--wa-color-surface-lowered) p-3 text-xs">${this.preview}</pre>`
            : ''}
          ${!up
            ? html`<wa-callout variant="warning" size="s"
                >The agent removes the file, runs the config test and reloads OpenResty.</wa-callout
              >`
            : ''}
        </form>
        ${up
          ? html`<wa-button slot="footer" appearance="outlined" ?disabled=${!this.template} @click=${this.renderPreview}>
              <wa-icon slot="start" name="eye"></wa-icon>Preview
            </wa-button>`
          : ''}
        <wa-button slot="footer" @click=${this.close}>Cancel</wa-button>
        <wa-button slot="footer" type="submit" form="deploy-form" variant=${up ? 'brand' : 'danger'} ?loading=${this.busy}>
          <wa-icon slot="start" name=${up ? 'up' : 'down'}></wa-icon>${up ? 'Bring up' : 'Take down'}
        </wa-button>
      </wa-dialog>
    `;
  }
}

import { html, nothing, type TemplateResult } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { api, type Directive, type Template } from '../api';
import { confirmAction } from '../components/confirm';
import { placeholders } from '../components/deploy-dialog';
import { LightElement, errorMessage, notify, session, timeAgo } from '../core';

const EXAMPLE = {
  name: 'my-site',
  description: '',
  variables: [
    { name: 'port', default: '80', required: false },
    { name: 'server_name', required: true },
  ],
  directives: [
    {
      name: 'server',
      children: [
        { name: 'listen', args: ['{{port}}'] },
        { name: 'server_name', args: ['{{server_name}}'] },
        { name: 'location', args: ['/'], children: [{ name: 'return', args: ['200', '"ok"'] }] },
      ],
    },
  ],
};

/** Strips server-assigned fields so a tree can be edited and sent back. */
function clean(d: Directive): Directive {
  const out: Directive = { name: d.name };
  if (d.args?.length) out.args = d.args;
  if (d.block && !d.children?.length && d.raw === undefined) out.block = true;
  if (d.raw !== undefined) out.raw = d.raw;
  if (d.comment) out.comment = d.comment;
  if (d.children?.length) out.children = d.children.map(clean);
  return out;
}

/** Renders a directive tree as indented, syntax-highlighted nginx config. */
function tree(ds: Directive[] = [], depth = 0): TemplateResult[] {
  const pad = '    '.repeat(depth);
  const hl = (s: string) =>
    s.split(/(\{\{\s*[A-Za-z_][A-Za-z0-9_]*\s*\}\})/).map((part, i) =>
      i % 2 ? html`<span class="rounded bg-(--wa-color-warning-fill-quiet) text-(--wa-color-warning-on-quiet)">${part}</span>` : part,
    );
  return ds.map((d) => {
    const head = html`${d.comment ? html`<span class="text-(--wa-color-text-quiet)">${pad}# ${d.comment}\n</span>` : nothing}${pad}<span
        class="font-semibold text-(--wa-color-brand-on-quiet)"
        >${d.name}</span
      >${(d.args ?? []).map((a) => html` ${hl(a)}`)}`;
    if (d.raw !== undefined) {
      const body = d.raw
        .split('\n')
        .map((l) => `${pad}    ${l}`)
        .join('\n');
      return html`${head} {\n<span class="text-(--wa-color-text-quiet)">${hl(body)}</span>\n${pad}}\n`;
    }
    if (d.children?.length || d.block) return html`${head} {\n${tree(d.children, depth + 1)}${pad}}\n`;
    return html`${head};\n`;
  });
}

@customElement('templates-page')
export class TemplatesPage extends LightElement {
  @state() private templates: Template[] = [];
  @state() private loading = true;
  @state() private selected: Template | null = null;
  @state() private values: Record<string, string> = {};
  @state() private preview = '';
  @state() private previewError = '';

  @state() private editorOpen = false;
  @state() private editingId: number | null = null;
  @state() private editorText = '';
  @state() private editorError = '';
  @state() private saving = false;

  override connectedCallback() {
    super.connectedCallback();
    this.load();
  }

  private async load(selectId?: number) {
    try {
      this.templates = await api.listTemplates();
      const id = selectId ?? this.selected?.id ?? this.templates[0]?.id;
      if (id !== undefined && this.templates.some((t) => t.id === id)) await this.select(id);
      else this.selected = null;
    } catch (e) {
      notify(errorMessage(e), 'danger');
    } finally {
      this.loading = false;
    }
  }

  private async select(id: number) {
    try {
      this.selected = await api.getTemplate(id);
      this.values = {};
      this.preview = '';
      this.previewError = '';
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
  }

  private async renderPreview() {
    if (!this.selected) return;
    const values = Object.fromEntries(Object.entries(this.values).filter(([, v]) => v !== ''));
    try {
      this.preview = (await api.renderTemplate(this.selected.id, values)).config;
      this.previewError = '';
    } catch (e) {
      this.preview = '';
      this.previewError = errorMessage(e);
    }
  }

  private openEditor(t: Template | null) {
    this.editingId = t?.id ?? null;
    const doc = t
      ? { name: t.name, description: t.description, variables: t.variables, directives: (t.directives ?? []).map(clean) }
      : EXAMPLE;
    this.editorText = JSON.stringify(doc, null, 2);
    this.editorError = '';
    this.editorOpen = true;
  }

  private async save() {
    let doc: unknown;
    try {
      doc = JSON.parse(this.editorText);
    } catch (e) {
      this.editorError = `Invalid JSON: ${errorMessage(e)}`;
      return;
    }
    this.saving = true;
    try {
      const t = this.editingId ? await api.replaceTemplate(this.editingId, doc) : await api.createTemplate(doc);
      notify(`Template ${t.name} saved`, 'success');
      this.editorOpen = false;
      await this.load(t.id);
    } catch (e) {
      const field = (e as { field?: string }).field;
      this.editorError = field ? `${field}: ${errorMessage(e)}` : errorMessage(e);
    } finally {
      this.saving = false;
    }
  }

  private async confirmDelete(t: Template) {
    const ok = await confirmAction({
      title: 'Delete template',
      body: html`Delete <strong>${t.name}</strong>? Configs already deployed from it stay in place.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteTemplate(t.id);
      notify(`Template ${t.name} deleted`, 'success');
      this.selected = null;
      await this.load();
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
  }

  private renderDetail() {
    const t = this.selected;
    if (!t) return html`<wa-callout variant="neutral">Select a template.</wa-callout>`;
    const declared = new Map(t.variables.map((v) => [v.name, v]));
    const names = [...new Set([...declared.keys(), ...placeholders(t.directives)])];
    return html`
      <wa-card class="min-w-0">
        <div slot="header" class="flex flex-wrap items-center gap-2">
          <div class="mr-auto">
            <h2 class="text-lg font-semibold">${t.name}</h2>
            <p class="text-xs text-(--wa-color-text-quiet)">Updated ${timeAgo(t.updated_at)}</p>
          </div>
          ${session.canWrite
            ? html`<wa-button size="s" appearance="outlined" @click=${() => this.openEditor(t)}>
                  <wa-icon slot="start" name="edit"></wa-icon>Edit
                </wa-button>
                <wa-button size="s" appearance="outlined" variant="danger" @click=${() => this.confirmDelete(t)}>
                  <wa-icon slot="start" name="trash"></wa-icon>Delete
                </wa-button>`
            : nothing}
        </div>
        <div class="flex flex-col gap-6">
          ${t.description ? html`<p class="text-sm">${t.description}</p>` : nothing}
          <section>
            <h3 class="mb-2 text-sm font-semibold">Directives</h3>
            <pre class="overflow-x-auto rounded-lg bg-(--wa-color-surface-lowered) p-4 text-xs leading-relaxed">${tree(t.directives)}</pre>
          </section>
          <section>
            <h3 class="mb-2 text-sm font-semibold">Preview</h3>
            ${names.length
              ? html`<div class="mb-3 grid gap-3 sm:grid-cols-2">
                  ${names.map((n) => {
                    const v = declared.get(n);
                    return html`<wa-input
                      size="s"
                      label=${n}
                      placeholder=${v?.default ?? ''}
                      hint=${v?.description ?? (v ? (v.required && v.default === undefined ? 'required' : '') : 'undeclared, required')}
                      .value=${this.values[n] ?? ''}
                      @input=${(e: Event) => (this.values = { ...this.values, [n]: (e.target as HTMLInputElement).value })}
                    ></wa-input>`;
                  })}
                </div>`
              : nothing}
            <wa-button size="s" appearance="outlined" @click=${this.renderPreview}>
              <wa-icon slot="start" name="eye"></wa-icon>Render
            </wa-button>
            ${this.previewError ? html`<wa-callout class="mt-3" variant="danger" size="s">${this.previewError}</wa-callout>` : nothing}
            ${this.preview
              ? html`<div class="relative mt-3">
                  <wa-copy-button class="absolute top-2 right-2" value=${this.preview}></wa-copy-button>
                  <pre class="overflow-x-auto rounded-lg bg-(--wa-color-surface-lowered) p-4 text-xs">${this.preview}</pre>
                </div>`
              : nothing}
          </section>
        </div>
      </wa-card>
    `;
  }

  private renderEditor() {
    return html`
      <wa-dialog
        label=${this.editingId ? 'Edit template' : 'New template'}
        style="--width: 52rem"
        ?open=${this.editorOpen}
        @wa-after-hide=${(e: Event) => e.target === e.currentTarget && (this.editorOpen = false)}
      >
        <div class="flex flex-col gap-3">
          <p class="text-sm text-(--wa-color-text-quiet)">
            A directive with <code>children</code> renders as a block; <code>raw</code> is copied verbatim (for
            <code>*_by_lua_block</code>). Use <code>{{name}}</code> placeholders in args. Saving replaces the whole tree.
          </p>
          ${this.editorError ? html`<wa-callout variant="danger" size="s">${this.editorError}</wa-callout>` : nothing}
          <wa-textarea
            rows="22"
            resize="vertical"
            spellcheck="false"
            class="font-mono [&::part(textarea)]:font-mono [&::part(textarea)]:text-xs"
            .value=${this.editorText}
            @input=${(e: Event) => (this.editorText = (e.target as HTMLTextAreaElement).value)}
          ></wa-textarea>
        </div>
        <wa-button slot="footer" @click=${() => (this.editorOpen = false)}>Cancel</wa-button>
        <wa-button slot="footer" variant="brand" ?loading=${this.saving} @click=${this.save}>Save</wa-button>
      </wa-dialog>
    `;
  }

  override render() {
    return html`
      <div class="mb-6 flex items-center gap-3">
        <h1 class="mr-auto text-2xl font-semibold">Templates</h1>
        ${session.canWrite
          ? html`<wa-button size="s" variant="brand" @click=${() => this.openEditor(null)}>
              <wa-icon slot="start" name="plus"></wa-icon>New template
            </wa-button>`
          : nothing}
      </div>
      ${this.loading
        ? html`<div class="grid place-items-center py-16"><wa-spinner class="text-2xl"></wa-spinner></div>`
        : this.templates.length === 0
          ? html`<wa-callout variant="neutral">No templates yet.</wa-callout>`
          : html`<div class="grid gap-6 lg:grid-cols-[16rem_1fr]">
              <nav class="flex flex-col gap-1">
                ${this.templates.map(
                  (t) => html`<button
                    class="flex h-auto w-full flex-col items-start rounded-lg border-0 px-3 py-2 text-left text-sm transition-colors ${this.selected?.id === t.id
                      ? 'bg-(--wa-color-brand-fill-quiet) font-medium text-(--wa-color-brand-on-quiet)'
                      : 'bg-transparent hover:bg-(--wa-color-neutral-fill-quiet)'}"
                    @click=${() => this.select(t.id)}
                  >
                    <div>${t.name}</div>
                    ${t.description ? html`<div class="truncate text-xs text-(--wa-color-text-quiet)">${t.description}</div>` : nothing}
                  </button>`,
                )}
              </nav>
              ${this.renderDetail()}
            </div>`}
      ${this.renderEditor()}
    `;
  }
}

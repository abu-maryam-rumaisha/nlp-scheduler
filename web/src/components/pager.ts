import { html, nothing, type TemplateResult } from 'lit';

export const PAGE_SIZES = [10, 20, 50, 100];

export interface PageState {
  page: number;
  pageSize: number;
}

/** The items on the current page of an in-memory list. */
export function pageOf<T>(items: T[], { page, pageSize }: PageState): T[] {
  return items.slice((page - 1) * pageSize, page * pageSize);
}

/** Keeps page within range after the list shrinks (filtering, deletes). */
export function clampPage(state: PageState, total: number): PageState {
  const last = Math.max(1, Math.ceil(total / state.pageSize));
  return state.page > last ? { ...state, page: last } : state;
}

/**
 * A bordered table card with a pagination footer: page buttons, a
 * "1–20 of 134" summary and a page-size picker. onChange gets the new state;
 * changing the page size returns to the first page.
 */
export function pagedTable(opts: {
  label: string;
  table: TemplateResult;
  total: number;
  state: PageState;
  onChange: (s: PageState) => void;
}) {
  const { label, table, total, state, onChange } = opts;
  return html`
    <div class="overflow-hidden rounded-xl border border-(--wa-color-surface-border) bg-(--wa-color-surface-default)">
      <div class="overflow-x-auto">${table}</div>
      ${total > 0
        ? html`<div
            class="flex flex-wrap items-center justify-between gap-3 border-t border-(--wa-color-surface-border) px-4 py-2.5"
          >
            <wa-pagination
              label=${label}
              with-summary
              .total=${total}
              .pageSize=${state.pageSize}
              .page=${state.page}
              @wa-page-change=${(e: CustomEvent<{ page: number }>) => onChange({ ...state, page: e.detail.page })}
            ></wa-pagination>
            <label class="flex items-center gap-2 text-sm text-(--wa-color-text-quiet)">
              Rows per page
              <wa-select
                size="s"
                class="w-20"
                .value=${String(state.pageSize)}
                @change=${(e: Event) => onChange({ page: 1, pageSize: Number((e.target as HTMLInputElement).value) })}
              >
                ${PAGE_SIZES.map((n) => html`<wa-option value=${String(n)}>${n}</wa-option>`)}
              </wa-select>
            </label>
          </div>`
        : nothing}
    </div>
  `;
}

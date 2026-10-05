import { html, render, type TemplateResult } from 'lit';

/**
 * Shows a modal confirmation and resolves to true if the user confirms.
 * The dialog is created on demand and removed when closed.
 */
export function confirmAction(opts: {
  title: string;
  body: string | TemplateResult;
  confirmLabel: string;
  danger?: boolean;
}): Promise<boolean> {
  return new Promise((resolve) => {
    const host = document.createElement('div');
    document.body.append(host);
    let confirmed = false;
    const close = (ok: boolean) => {
      confirmed = ok;
      (host.querySelector('wa-dialog') as HTMLElement & { open: boolean }).open = false;
    };
    render(
      html`
        <wa-dialog
          label=${opts.title}
          open
          @wa-after-hide=${(e: Event) => {
            if (e.target !== e.currentTarget) return;
            host.remove();
            resolve(confirmed);
          }}
        >
          <div class="text-sm">${opts.body}</div>
          <wa-button slot="footer" @click=${() => close(false)}>Cancel</wa-button>
          <wa-button slot="footer" variant=${opts.danger ? 'danger' : 'brand'} @click=${() => close(true)}
            >${opts.confirmLabel}</wa-button
          >
        </wa-dialog>
      `,
      host,
    );
  });
}

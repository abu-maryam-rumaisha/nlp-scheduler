import { html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { api, type TOTPSetup } from '../api';
import { LightElement, errorMessage, notify, session, valueOf } from '../core';

/**
 * Turns two-factor authentication with an authenticator app (Google
 * Authenticator and the like) on or off for the signed-in user. Emits
 * `closed`.
 */
@customElement('totp-dialog')
export class TOTPDialog extends LightElement {
  @property({ type: Boolean }) open = false;
  @state() private setup: TOTPSetup | null = null;
  @state() private busy = false;
  @state() private error = '';

  override updated(changed: Map<string, unknown>) {
    if (changed.has('open') && this.open && !session.user?.totp_enabled) this.startSetup();
  }

  private async startSetup() {
    this.setup = null;
    this.error = '';
    try {
      this.setup = await api.setupTOTP();
    } catch (err) {
      this.error = errorMessage(err);
    }
  }

  private close() {
    this.error = '';
    this.setup = null;
    this.querySelector('form')?.reset();
    this.dispatchEvent(new Event('closed'));
  }

  private async submit(e: SubmitEvent) {
    e.preventDefault();
    this.busy = true;
    this.error = '';
    try {
      if (session.user?.totp_enabled) {
        session.set(await api.disableTOTP(valueOf(this, '#totp-password'), valueOf(this, '#totp-code')));
        notify('Two-factor authentication is off.', 'success');
      } else {
        const { user } = await api.enableTOTP(valueOf(this, '#totp-code'));
        session.set(user);
        notify('Two-factor authentication is on. Other sessions were signed out.', 'success');
      }
      this.close();
    } catch (err) {
      this.error = errorMessage(err);
    } finally {
      this.busy = false;
    }
  }

  private codeInput() {
    return html`<wa-input
      id="totp-code"
      label="Code from the app"
      inputmode="numeric"
      autocomplete="one-time-code"
      pattern="[0-9 ]{6,7}"
      maxlength="7"
      required
    ></wa-input>`;
  }

  private enableBody() {
    if (!this.setup) {
      return this.error ? nothing : html`<div class="grid place-items-center py-8"><wa-spinner class="text-2xl"></wa-spinner></div>`;
    }
    return html`
      <ol class="flex list-decimal flex-col gap-2 pl-5 text-sm">
        <li>Open Google Authenticator (or any authenticator app) and add an account.</li>
        <li>Scan this QR code:</li>
      </ol>
      <img src=${this.setup.qr_code} alt="QR code for your authenticator app" class="mx-auto size-48 rounded-lg bg-white p-2" />
      <details class="text-sm">
        <summary class="cursor-pointer text-(--wa-color-text-quiet)">Can't scan it? Enter this key instead</summary>
        <code class="mt-2 block font-mono break-all select-all">${this.setup.secret.match(/.{1,4}/g)?.join(' ')}</code>
      </details>
      <ol start="3" class="list-decimal pl-5 text-sm"><li>Enter the 6-digit code the app shows to finish.</li></ol>
      ${this.codeInput()}
    `;
  }

  private disableBody() {
    return html`
      <p class="text-sm">Two-factor authentication is on. To turn it off, confirm your password and a current code.</p>
      <wa-input id="totp-password" label="Password" type="password" required autocomplete="current-password"></wa-input>
      ${this.codeInput()}
    `;
  }

  override render() {
    const enabled = !!session.user?.totp_enabled;
    return html`
      <wa-dialog
        label="Two-factor authentication"
        ?open=${this.open}
        @wa-after-hide=${(e: Event) => e.target === e.currentTarget && this.close()}
      >
        ${this.open
          ? html`<form id="totp-form" class="flex flex-col gap-4" @submit=${this.submit}>
              ${this.error ? html`<wa-callout variant="danger" size="s">${this.error}</wa-callout>` : nothing}
              ${enabled ? this.disableBody() : this.enableBody()}
            </form>`
          : nothing}
        <wa-button slot="footer" @click=${this.close}>Cancel</wa-button>
        <wa-button
          slot="footer"
          variant=${enabled ? 'danger' : 'brand'}
          type="submit"
          form="totp-form"
          ?disabled=${!enabled && !this.setup}
          ?loading=${this.busy}
          >${enabled ? 'Turn off' : 'Turn on'}</wa-button
        >
      </wa-dialog>
    `;
  }
}

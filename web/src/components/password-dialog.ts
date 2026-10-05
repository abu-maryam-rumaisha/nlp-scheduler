import { html } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { api } from '../api';
import { LightElement, errorMessage, notify, session, valueOf } from '../core';

/** Lets the signed-in user change their own password. Emits `closed`. */
@customElement('password-dialog')
export class PasswordDialog extends LightElement {
  @property({ type: Boolean }) open = false;
  @state() private busy = false;
  @state() private error = '';

  private close() {
    this.error = '';
    this.querySelector('form')?.reset();
    this.dispatchEvent(new Event('closed'));
  }

  private async submit(e: SubmitEvent) {
    e.preventDefault();
    const next = valueOf(this, '#new-password');
    if (next !== valueOf(this, '#confirm-password')) {
      this.error = 'The new passwords do not match.';
      return;
    }
    this.busy = true;
    this.error = '';
    try {
      const { user } = await api.changePassword(valueOf(this, '#current-password'), next);
      session.set(user);
      notify('Password changed. Other sessions were signed out.', 'success');
      this.close();
    } catch (err) {
      this.error = errorMessage(err);
    } finally {
      this.busy = false;
    }
  }

  override render() {
    return html`
      <wa-dialog
        label="Change password"
        ?open=${this.open}
        @wa-after-hide=${(e: Event) => e.target === e.currentTarget && this.close()}
      >
        <form id="password-form" class="flex flex-col gap-4" @submit=${this.submit}>
          ${this.error ? html`<wa-callout variant="danger" size="s">${this.error}</wa-callout>` : ''}
          <wa-input id="current-password" label="Current password" type="password" required autocomplete="current-password"></wa-input>
          <wa-input id="new-password" label="New password" type="password" required minlength="8" autocomplete="new-password" hint="At least 8 characters"></wa-input>
          <wa-input id="confirm-password" label="Confirm new password" type="password" required autocomplete="new-password"></wa-input>
        </form>
        <wa-button slot="footer" @click=${this.close}>Cancel</wa-button>
        <wa-button slot="footer" variant="brand" type="submit" form="password-form" ?loading=${this.busy}>Change password</wa-button>
      </wa-dialog>
    `;
  }
}

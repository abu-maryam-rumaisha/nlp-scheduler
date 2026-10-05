import { html } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { api } from '../api';
import { LightElement, errorMessage, router, session, valueOf } from '../core';

@customElement('login-page')
export class LoginPage extends LightElement {
  @state() private busy = false;
  @state() private error = '';

  private async submit(e: SubmitEvent) {
    e.preventDefault();
    this.busy = true;
    this.error = '';
    try {
      const { user } = await api.login(valueOf(this, '#username'), valueOf(this, '#password'));
      session.set(user);
      router.go('/instances', true);
    } catch (err) {
      this.error = errorMessage(err);
    } finally {
      this.busy = false;
    }
  }

  override render() {
    return html`
      <div class="grid min-h-screen place-items-center p-4">
        <wa-card class="w-full max-w-sm">
          <div class="mb-6 flex items-center gap-3">
            <span class="grid size-10 place-items-center rounded-xl bg-(--wa-color-brand-fill-loud) font-semibold text-white"
              >OR</span
            >
            <div>
              <h1 class="text-lg font-semibold">OpenResty Manager</h1>
              <p class="text-sm text-(--wa-color-text-quiet)">Sign in to continue</p>
            </div>
          </div>
          <form class="flex flex-col gap-4" @submit=${this.submit}>
            ${this.error ? html`<wa-callout variant="danger" size="s">${this.error}</wa-callout>` : ''}
            <wa-input id="username" label="Username" autocomplete="username" required autofocus></wa-input>
            <wa-input
              id="password"
              label="Password"
              type="password"
              autocomplete="current-password"
              password-toggle
              required
            ></wa-input>
            <wa-button type="submit" variant="brand" ?loading=${this.busy}>Sign in</wa-button>
          </form>
        </wa-card>
      </div>
    `;
  }
}

import { html } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { api, ApiError } from '../api';
import { LightElement, errorMessage, router, session, valueOf } from '../core';

@customElement('login-page')
export class LoginPage extends LightElement {
  @state() private busy = false;
  @state() private error = '';
  /** Password accepted; waiting for the authenticator code. */
  @state() private needsCode = false;
  private username = '';
  private password = '';

  private async submit(e: SubmitEvent) {
    e.preventDefault();
    if (!this.needsCode) {
      this.username = valueOf(this, '#username');
      this.password = valueOf(this, '#password');
    }
    this.busy = true;
    this.error = '';
    try {
      const code = this.needsCode ? valueOf(this, '#code') : undefined;
      const { user } = await api.login(this.username, this.password, code);
      this.password = '';
      session.set(user);
      router.go('/instances', true);
    } catch (err) {
      if (!this.needsCode && err instanceof ApiError && err.field === 'code') {
        this.needsCode = true;
      } else {
        this.error = errorMessage(err);
      }
    } finally {
      this.busy = false;
    }
  }

  private back() {
    this.needsCode = false;
    this.password = '';
    this.error = '';
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
              <p class="text-sm text-(--wa-color-text-quiet)">
                ${this.needsCode ? 'Two-factor authentication' : 'Sign in to continue'}
              </p>
            </div>
          </div>
          <form class="flex flex-col gap-4" @submit=${this.submit}>
            ${this.error ? html`<wa-callout variant="danger" size="s">${this.error}</wa-callout>` : ''}
            ${this.needsCode
              ? html`
                  <wa-input
                    id="code"
                    label="Authenticator code"
                    hint="Open your authenticator app and enter the 6-digit code for ${this.username}."
                    inputmode="numeric"
                    autocomplete="one-time-code"
                    pattern="[0-9 ]{6,7}"
                    maxlength="7"
                    required
                    autofocus
                  ></wa-input>
                  <wa-button type="submit" variant="brand" ?loading=${this.busy}>Verify</wa-button>
                  <wa-button appearance="plain" size="s" @click=${this.back}>Back</wa-button>
                `
              : html`
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
                `}
          </form>
        </wa-card>
      </div>
    `;
  }
}

import { html, nothing } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { api } from '../api';
import { displayName, userAvatar } from '../components/avatar';
import { LightElement, errorMessage, notify, session, timeAgo, valueOf } from '../core';

import '../components/totp-dialog';

/** The signed-in user's own account: profile, password and two-factor authentication. */
@customElement('settings-page')
export class SettingsPage extends LightElement {
  @state() private totpOpen = false;
  @state() private profileBusy = false;
  @state() private profileError = '';
  @state() private passwordBusy = false;
  @state() private passwordError = '';

  private async saveProfile(e: SubmitEvent) {
    e.preventDefault();
    const form = e.target as HTMLFormElement;
    this.profileBusy = true;
    this.profileError = '';
    try {
      session.set(
        await api.updateProfile({
          display_name: valueOf(form, '[name=display_name]'),
          email: valueOf(form, '[name=email]'),
        }),
      );
      notify('Profile saved', 'success');
    } catch (err) {
      this.profileError = errorMessage(err);
    } finally {
      this.profileBusy = false;
    }
  }

  private async changePassword(e: SubmitEvent) {
    e.preventDefault();
    const form = e.target as HTMLFormElement;
    const next = valueOf(form, '[name=new_password]');
    if (next !== valueOf(form, '[name=confirm_password]')) {
      this.passwordError = 'The new passwords do not match.';
      return;
    }
    this.passwordBusy = true;
    this.passwordError = '';
    try {
      const { user } = await api.changePassword(valueOf(form, '[name=current_password]'), next);
      session.set(user);
      form.reset();
      notify('Password changed. Other sessions were signed out.', 'success');
    } catch (err) {
      this.passwordError = errorMessage(err);
    } finally {
      this.passwordBusy = false;
    }
  }

  private card(title: string, description: unknown, body: unknown) {
    return html`
      <wa-card>
        <h2 class="font-semibold">${title}</h2>
        <p class="mt-1 mb-4 text-sm text-(--wa-color-text-quiet)">${description}</p>
        ${body}
      </wa-card>
    `;
  }

  private error(msg: string) {
    return msg ? html`<wa-callout variant="danger" size="s">${msg}</wa-callout>` : nothing;
  }

  override render() {
    const user = session.user!;
    const totp = user.totp_enabled;
    return html`
      <h1 class="mb-6 text-2xl font-semibold">Settings</h1>
      <div class="flex max-w-3xl flex-col gap-4">
        <wa-card>
          <div class="flex items-center gap-4">
            ${userAvatar(user, '4rem')}
            <div class="min-w-0">
              <div class="truncate text-lg font-semibold">${displayName(user)}</div>
              <div class="truncate text-sm text-(--wa-color-text-quiet)">
                @${user.username}${user.email ? html` · ${user.email}` : nothing}
              </div>
              <div class="mt-1 text-xs text-(--wa-color-text-quiet)">Member since ${timeAgo(user.created_at)}</div>
            </div>
            <wa-badge
              class="ml-auto"
              variant=${user.role === 'prosecutor' ? 'brand' : 'neutral'}
              appearance="filled-outlined"
              >${user.role}</wa-badge
            >
          </div>
        </wa-card>

        ${this.card(
          'Profile',
          'How you appear in the app. Your username is used to sign in and cannot be changed here.',
          html`
            <form class="flex flex-col gap-4" @submit=${this.saveProfile}>
              ${this.error(this.profileError)}
              <wa-input label="Username" .value=${user.username} disabled></wa-input>
              <div class="grid gap-4 sm:grid-cols-2">
                <wa-input
                  name="display_name"
                  label="Display name"
                  .value=${user.display_name}
                  maxlength="100"
                  autocomplete="name"
                  placeholder=${user.username}
                ></wa-input>
                <wa-input name="email" label="Email" type="email" .value=${user.email} autocomplete="email"></wa-input>
              </div>
              <wa-button type="submit" variant="brand" class="self-end" ?loading=${this.profileBusy}>Save profile</wa-button>
            </form>
          `,
        )}
        ${this.card(
          'Password',
          'Changing your password signs out your other sessions.',
          html`
            <form class="flex flex-col gap-4" @submit=${this.changePassword}>
              ${this.error(this.passwordError)}
              <wa-input name="current_password" label="Current password" type="password" required autocomplete="current-password" password-toggle></wa-input>
              <div class="grid gap-4 sm:grid-cols-2">
                <wa-input name="new_password" label="New password" type="password" required minlength="8" autocomplete="new-password" password-toggle hint="At least 8 characters"></wa-input>
                <wa-input name="confirm_password" label="Confirm new password" type="password" required autocomplete="new-password" password-toggle></wa-input>
              </div>
              <wa-button type="submit" variant="brand" class="self-end" ?loading=${this.passwordBusy}>Change password</wa-button>
            </form>
          `,
        )}
        ${this.card(
          'Two-factor authentication',
          html`
            <span class="mb-1 flex items-center gap-2">
              Status:
              <wa-badge variant=${totp ? 'success' : 'neutral'} appearance="outlined">${totp ? 'on' : 'off'}</wa-badge>
            </span>
            ${totp
              ? 'Signing in asks for a code from your authenticator app.'
              : 'Ask for a code from an authenticator app such as Google Authenticator each time you sign in, in addition to your password.'}
          `,
          totp
            ? html`<wa-button size="s" variant="danger" appearance="outlined" @click=${() => (this.totpOpen = true)}>
                Turn off
              </wa-button>`
            : html`<wa-button size="s" variant="brand" @click=${() => (this.totpOpen = true)}>
                <wa-icon slot="start" name="shield"></wa-icon>Set up authenticator
              </wa-button>`,
        )}
      </div>
      <totp-dialog ?open=${this.totpOpen} @closed=${() => (this.totpOpen = false)}></totp-dialog>
    `;
  }
}

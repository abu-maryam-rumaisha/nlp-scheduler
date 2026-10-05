import { html } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { api, SESSION_EXPIRED } from '../api';
import { LightElement, errorMessage, notify, router, session } from '../core';

import '../pages/api-keys-page';
import '../pages/deployments-page';
import '../pages/instances-page';
import '../pages/login-page';
import '../pages/templates-page';
import '../pages/users-page';
import './password-dialog';

const NAV = [
  { path: '/instances', label: 'Instances', icon: 'server' },
  { path: '/deployments', label: 'Deployments', icon: 'rocket' },
  { path: '/templates', label: 'Templates', icon: 'file' },
  { path: '/api-keys', label: 'API keys', icon: 'key' },
  { path: '/users', label: 'Users', icon: 'users', prosecutorOnly: true },
];

@customElement('app-root')
export class AppRoot extends LightElement {
  @state() private ready = false;
  @state() private passwordOpen = false;

  override connectedCallback() {
    super.connectedCallback();
    router.addEventListener('change', () => this.requestUpdate());
    session.addEventListener('change', () => this.requestUpdate());
    window.addEventListener(SESSION_EXPIRED, () => {
      if (session.user) notify('Your session has expired. Please sign in again.', 'warning');
      session.set(null);
    });
    router.start();
    api
      .me()
      .then((u) => session.set(u))
      .catch(() => session.set(null))
      .finally(() => (this.ready = true));
  }

  private async logout() {
    try {
      await api.logout();
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
    session.set(null);
    router.go('/login', true);
  }

  private toggleTheme() {
    const dark = document.documentElement.classList.toggle('wa-dark');
    try {
      localStorage.setItem('theme', dark ? 'dark' : 'light');
    } catch {
      /* storage unavailable; the toggle still applies to this page */
    }
    this.requestUpdate();
  }

  private page() {
    const path = router.path;
    if (path.startsWith('/instances')) return html`<instances-page></instances-page>`;
    if (path.startsWith('/deployments')) return html`<deployments-page></deployments-page>`;
    if (path.startsWith('/templates')) return html`<templates-page></templates-page>`;
    if (path.startsWith('/api-keys')) return html`<api-keys-page></api-keys-page>`;
    if (path.startsWith('/users')) {
      return session.canWrite
        ? html`<users-page></users-page>`
        : html`<wa-callout variant="warning">Only prosecutors can manage users.</wa-callout>`;
    }
    return html`<wa-callout variant="neutral">Page not found. <a class="underline" href="/instances">Go to instances</a>.</wa-callout>`;
  }

  override render() {
    if (!this.ready) {
      return html`<div class="grid min-h-screen place-items-center"><wa-spinner class="text-3xl"></wa-spinner></div>`;
    }
    const user = session.user;
    if (!user) {
      if (router.path !== '/login') queueMicrotask(() => router.go('/login', true));
      return html`<login-page></login-page>`;
    }
    if (router.path === '/' || router.path === '/login') {
      queueMicrotask(() => router.go('/instances', true));
    }
    const dark = document.documentElement.classList.contains('wa-dark');

    return html`
      <div class="flex min-h-screen flex-col md:flex-row">
        <aside
          class="flex shrink-0 flex-row items-center gap-1 overflow-x-auto border-b border-(--wa-color-surface-border) bg-(--wa-color-surface-default) px-3 py-2 md:w-60 md:flex-col md:items-stretch md:border-r md:border-b-0 md:px-3 md:py-5"
        >
          <a href="/instances" class="mr-3 flex items-center gap-2 px-2 font-semibold md:mr-0 md:mb-6">
            <span class="grid size-8 place-items-center rounded-lg bg-(--wa-color-brand-fill-loud) text-white">OR</span>
            <span class="hidden sm:inline">OpenResty Manager</span>
          </a>
          ${NAV.filter((n) => !n.prosecutorOnly || session.canWrite).map(
            (n) => html`
              <a
                href=${n.path}
                class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm whitespace-nowrap transition-colors ${router.path.startsWith(
                  n.path,
                )
                  ? 'bg-(--wa-color-brand-fill-quiet) font-medium text-(--wa-color-brand-on-quiet)'
                  : 'text-(--wa-color-text-quiet) hover:bg-(--wa-color-neutral-fill-quiet)'}"
              >
                <wa-icon name=${n.icon}></wa-icon>${n.label}
              </a>
            `,
          )}
        </aside>

        <div class="flex min-w-0 flex-1 flex-col">
          <header
            class="flex items-center justify-end gap-2 border-b border-(--wa-color-surface-border) bg-(--wa-color-surface-default) px-4 py-2"
          >
            <wa-button appearance="plain" size="s" @click=${this.toggleTheme} title="Toggle dark mode">
              <wa-icon name=${dark ? 'sun' : 'moon'} label="Toggle dark mode"></wa-icon>
            </wa-button>
            <wa-dropdown
              @wa-select=${(e: CustomEvent<{ item: { value: string } }>) =>
                e.detail.item.value === 'logout' ? this.logout() : (this.passwordOpen = true)}
            >
              <wa-button slot="trigger" appearance="plain" size="s" with-caret>
                ${user.username}
                <wa-badge
                  slot="end"
                  variant=${user.role === 'prosecutor' ? 'brand' : 'neutral'}
                  appearance="filled-outlined"
                  >${user.role}</wa-badge
                >
              </wa-button>
              <wa-dropdown-item value="password"><wa-icon slot="icon" name="key"></wa-icon>Change password</wa-dropdown-item>
              <wa-dropdown-item value="logout" variant="danger"><wa-icon slot="icon" name="logout"></wa-icon>Sign out</wa-dropdown-item>
            </wa-dropdown>
          </header>
          <main class="w-full min-w-0 flex-1 p-4 md:p-6">${this.page()}</main>
        </div>
      </div>
      <password-dialog ?open=${this.passwordOpen} @closed=${() => (this.passwordOpen = false)}></password-dialog>
    `;
  }
}

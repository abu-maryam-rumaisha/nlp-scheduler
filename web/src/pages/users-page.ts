import { html, nothing } from 'lit';
import { customElement, state } from 'lit/decorators.js';
import { api, type Role, type User } from '../api';
import { confirmAction } from '../components/confirm';
import { clampPage, pageOf, pagedTable, type PageState } from '../components/pager';
import { LightElement, errorMessage, notify, session, timeAgo, valueOf } from '../core';

@customElement('users-page')
export class UsersPage extends LightElement {
  @state() private users: User[] = [];
  @state() private roles: Role[] = [];
  @state() private loading = true;
  @state() private paging: PageState = { page: 1, pageSize: 20 };

  @state() private createOpen = false;
  @state() private resetFor: User | null = null;
  @state() private formError = '';
  @state() private saving = false;

  override connectedCallback() {
    super.connectedCallback();
    this.load();
  }

  private async load() {
    try {
      [this.users, this.roles] = await Promise.all([api.listUsers(), api.listRoles()]);
    } catch (e) {
      notify(errorMessage(e), 'danger');
    } finally {
      this.loading = false;
    }
  }

  private async create(e: SubmitEvent) {
    e.preventDefault();
    const form = e.target as HTMLFormElement;
    this.saving = true;
    try {
      const u = await api.createUser({
        username: valueOf(form, '[name=username]'),
        password: valueOf(form, '[name=password]'),
        role: valueOf(form, '[name=role]'),
      });
      notify(`User ${u.username} created`, 'success');
      this.createOpen = false;
      await this.load();
    } catch (err) {
      this.formError = errorMessage(err);
    } finally {
      this.saving = false;
    }
  }

  private async resetPassword(e: SubmitEvent) {
    e.preventDefault();
    const u = this.resetFor!;
    this.saving = true;
    try {
      await api.updateUser(u.id, { password: valueOf(e.target as HTMLFormElement, '[name=password]') });
      notify(`Password reset for ${u.username}; their sessions were signed out`, 'success');
      this.resetFor = null;
    } catch (err) {
      this.formError = errorMessage(err);
    } finally {
      this.saving = false;
    }
  }

  private async changeRole(u: User, role: string) {
    if (role === u.role) return;
    try {
      await api.updateUser(u.id, { role });
      notify(`${u.username} is now a ${role}`, 'success');
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
    await this.load();
  }

  private async confirmDelete(u: User) {
    const ok = await confirmAction({
      title: 'Delete user',
      body: html`Delete <strong>${u.username}</strong>? They are signed out immediately.`,
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteUser(u.id);
      notify(`User ${u.username} deleted`, 'success');
      await this.load();
    } catch (e) {
      notify(errorMessage(e), 'danger');
    }
  }

  private dialog(label: string, open: boolean, onClose: () => void, formId: string, body: unknown, submit: (e: SubmitEvent) => void) {
    return html`
      <wa-dialog label=${label} ?open=${open} @wa-after-hide=${(e: Event) => e.target === e.currentTarget && onClose()}>
        ${open
          ? html`<form id=${formId} class="flex flex-col gap-4" @submit=${submit}>
              ${this.formError ? html`<wa-callout variant="danger" size="s">${this.formError}</wa-callout>` : nothing} ${body}
            </form>`
          : nothing}
        <wa-button slot="footer" @click=${onClose}>Cancel</wa-button>
        <wa-button slot="footer" variant="brand" type="submit" form=${formId} ?loading=${this.saving}>Save</wa-button>
      </wa-dialog>
    `;
  }

  override render() {
    const me = session.user!;
    return html`
      <div class="mb-6 flex items-center gap-3">
        <h1 class="mr-auto text-2xl font-semibold">Users</h1>
        <wa-button
          size="s"
          variant="brand"
          @click=${() => {
            this.formError = '';
            this.createOpen = true;
          }}
        >
          <wa-icon slot="start" name="plus"></wa-icon>New user
        </wa-button>
      </div>

      ${this.loading
        ? html`<div class="grid place-items-center py-16"><wa-spinner class="text-2xl"></wa-spinner></div>`
        : pagedTable({
            label: 'Users',
            total: this.users.length,
            state: clampPage(this.paging, this.users.length),
            onChange: (st) => (this.paging = st),
            table: html`
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Username</th>
                    <th>Role</th>
                    <th>Created</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  ${pageOf(this.users, clampPage(this.paging, this.users.length)).map(
                    (u) => html`
                      <tr>
                        <td class="font-medium">
                          ${u.username}
                          ${u.id === me.id ? html`<wa-badge variant="neutral" appearance="outlined" class="ml-2">you</wa-badge>` : nothing}
                        </td>
                        <td>
                          <wa-select
                            size="s"
                            class="w-40"
                            .value=${u.role}
                            ?disabled=${u.id === me.id}
                            @change=${(e: Event) => this.changeRole(u, (e.target as HTMLInputElement).value)}
                          >
                            ${this.roles.map((r) => html`<wa-option value=${r.name}>${r.name}</wa-option>`)}
                          </wa-select>
                        </td>
                        <td class="text-(--wa-color-text-quiet)">${timeAgo(u.created_at)}</td>
                        <td class="text-right whitespace-nowrap">
                          <wa-button
                            size="s"
                            appearance="plain"
                            title="Reset password"
                            @click=${() => {
                              this.formError = '';
                              this.resetFor = u;
                            }}
                          >
                            <wa-icon name="key" label="Reset password"></wa-icon>
                          </wa-button>
                          <wa-button
                            size="s"
                            appearance="plain"
                            variant="danger"
                            title="Delete"
                            ?disabled=${u.id === me.id}
                            @click=${() => this.confirmDelete(u)}
                          >
                            <wa-icon name="trash" label="Delete"></wa-icon>
                          </wa-button>
                        </td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            `,
          })}
      ${this.dialog(
        'New user',
        this.createOpen,
        () => (this.createOpen = false),
        'create-user-form',
        html`
          <wa-input name="username" label="Username" required autocomplete="off"></wa-input>
          <wa-input name="password" label="Password" type="password" required minlength="8" autocomplete="new-password" password-toggle hint="At least 8 characters"></wa-input>
          <wa-select name="role" label="Role" value=${this.roles[0]?.name ?? ''} required>
            ${this.roles.map((r) => html`<wa-option value=${r.name}>${r.name}</wa-option>`)}
          </wa-select>
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs text-(--wa-color-text-quiet)">
            ${this.roles.map((r) => html`<dt class="font-medium">${r.name}</dt><dd>${r.description}</dd>`)}
          </dl>
        `,
        (e) => this.create(e),
      )}
      ${this.dialog(
        `Reset password for ${this.resetFor?.username ?? ''}`,
        !!this.resetFor,
        () => (this.resetFor = null),
        'reset-password-form',
        html`<wa-input name="password" label="New password" type="password" required minlength="8" autocomplete="new-password" password-toggle></wa-input>`,
        (e) => this.resetPassword(e),
      )}
    `;
  }
}

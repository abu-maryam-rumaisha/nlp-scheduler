// Small shared pieces: light-DOM base element, session state, router and
// notifications.
import { LitElement } from 'lit';
import type { User } from './api';

/**
 * Renders into light DOM so the global Tailwind stylesheet applies.
 * Web Awesome components still use their own shadow roots.
 */
export class LightElement extends LitElement {
  protected override createRenderRoot() {
    return this;
  }
}

// --- session ---------------------------------------------------------------

class Session extends EventTarget {
  user: User | null = null;

  set(user: User | null) {
    this.user = user;
    this.dispatchEvent(new Event('change'));
  }

  get canWrite() {
    return this.user?.role === 'prosecutor';
  }
}

export const session = new Session();

// --- router ----------------------------------------------------------------

export const ROUTES = ['/instances', '/deployments', '/templates', '/users', '/settings'] as const;

class Router extends EventTarget {
  get path() {
    return location.pathname;
  }

  go(path: string, replace = false) {
    if (path === location.pathname) return;
    if (replace) history.replaceState(null, '', path);
    else history.pushState(null, '', path);
    this.dispatchEvent(new Event('change'));
  }

  start() {
    window.addEventListener('popstate', () => this.dispatchEvent(new Event('change')));
    // Turn same-origin <a href="/..."> clicks into client-side navigation.
    document.addEventListener('click', (e) => {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const a = (e.composedPath() as Element[]).find((el) => el instanceof HTMLAnchorElement) as
        | HTMLAnchorElement
        | undefined;
      if (!a || a.target || a.origin !== location.origin || a.hasAttribute('download')) return;
      e.preventDefault();
      this.go(a.pathname + a.search);
    });
  }
}

export const router = new Router();

// --- notifications ---------------------------------------------------------

type Variant = 'brand' | 'success' | 'warning' | 'danger' | 'neutral';

export function notify(message: string, variant: Variant = 'neutral') {
  const toast = document.querySelector('wa-toast') as
    | (HTMLElement & { create(m: string, o: { variant: Variant; duration: number }): void })
    | null;
  toast?.create(message, { variant, duration: variant === 'danger' ? 8000 : 5000 });
}

export function errorMessage(e: unknown) {
  return e instanceof Error ? e.message : String(e);
}

// --- formatting --------------------------------------------------------------

export function timeAgo(iso: string | null | undefined) {
  if (!iso) return 'never';
  const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 5) return 'just now';
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return new Date(iso).toLocaleString();
}

/** Reads the value of a Web Awesome form control by id inside root. */
export function valueOf(root: ParentNode, selector: string): string {
  const el = root.querySelector(selector) as (HTMLElement & { value: string | string[] }) | null;
  const v = el?.value ?? '';
  return Array.isArray(v) ? v.join(',') : v;
}

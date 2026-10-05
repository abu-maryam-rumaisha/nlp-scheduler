import { html } from 'lit';
import type { User } from '../api';

/** The name to show for a user: their display name, else the username. */
export const displayName = (u: User) => u.display_name || u.username;

/** Up to two initials, from the first and last word of the name. */
function initials(name: string) {
  const words = name.split(/[\s._-]+/).filter(Boolean);
  const first = words[0]?.[0] ?? '?';
  const last = words.length > 1 ? words[words.length - 1][0] : '';
  return (first + last).toUpperCase();
}

/** A round avatar with the user's initials. `size` is a CSS length. */
export function userAvatar(u: User, size = '2rem') {
  const name = displayName(u);
  return html`<wa-avatar
    shape="circle"
    initials=${initials(name)}
    label=${name}
    style="--size: ${size}; background-color: var(--wa-color-brand-fill-loud); color: var(--wa-color-brand-on-loud)"
  ></wa-avatar>`;
}

import { html } from 'lit';
import type { DeploymentStatus } from '../api';

const variants: Record<DeploymentStatus, string> = {
  pending: 'neutral',
  published: 'brand',
  applied: 'success',
  failed: 'danger',
};

export const statusBadge = (s: DeploymentStatus) =>
  html`<wa-badge variant=${variants[s]} appearance="filled-outlined" attention=${s === 'published' ? 'pulse' : 'none'} pill>${s}</wa-badge>`;

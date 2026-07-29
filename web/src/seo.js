import React from 'react';

const DEFAULT_DESCRIPTION =
  'Tronvent is a self-hosted TRON chain monitor. Watch wallet addresses and TRC-20 contracts and receive signed webhooks — without running your own node.';
const DEFAULT_IMAGE = '/tronvent-logo.svg';

const PAGE_META = {
  '/': {
    title: 'Tronvent — Reliable TRON transaction events for your application',
    description:
      'Watch the wallets and tokens you care about on TRON. Tronvent monitors the network and sends signed webhooks to your application.',
  },
  '/docs/start': {
    title: 'Getting started — Tronvent docs',
    description:
      'Deploy Tronvent with Docker or Helm, configure TronGrid and webhooks, and start receiving TRON notifications.',
  },
  '/docs/docker': {
    title: 'Docker — Tronvent docs',
    description:
      'Run Tronvent with the official Docker image or Docker Compose on a single server.',
  },
  '/docs/kubernetes': {
    title: 'Kubernetes / Helm — Tronvent docs',
    description: 'Install Tronvent on Kubernetes using the official Helm chart from GHCR.',
  },
  '/docs/configure': {
    title: 'Configuration — Tronvent docs',
    description:
      'Environment variables and settings for TronGrid, PostgreSQL, admin access, and webhook delivery.',
  },
  '/docs/dashboard': {
    title: 'Dashboard & API — Tronvent docs',
    description:
      'Use the Tronvent dashboard and admin API to inspect watches, runtime status, and delivery health.',
  },
  '/docs/watch': {
    title: 'Watch activity — Tronvent docs',
    description: 'Register TRON wallet addresses and TRC-20 contracts to monitor for transfers.',
  },
  '/docs/webhooks': {
    title: 'Webhooks — Tronvent docs',
    description:
      'Receive signed JSON webhook notifications for TRX and TRC-20 transfers in your application.',
  },
};

function upsertMeta(selector, attributes) {
  let element = document.head.querySelector(selector);
  if (!element) {
    element = document.createElement('meta');
    document.head.appendChild(element);
  }
  Object.entries(attributes).forEach(([key, value]) => {
    element.setAttribute(key, value);
  });
}

function upsertLink(rel, href) {
  let element = document.head.querySelector(`link[rel="${rel}"]`);
  if (!element) {
    element = document.createElement('link');
    element.setAttribute('rel', rel);
    document.head.appendChild(element);
  }
  element.setAttribute('href', href);
}

function absoluteUrl(pathname) {
  if (typeof window === 'undefined') return pathname;
  return new URL(pathname, window.location.origin).href;
}

export function applyPageSeo(pathname) {
  const meta = PAGE_META[pathname] || PAGE_META['/'];
  const title = meta.title;
  const description = meta.description || DEFAULT_DESCRIPTION;
  const url = absoluteUrl(pathname);
  const image = absoluteUrl(DEFAULT_IMAGE);

  document.title = title;

  upsertMeta('meta[name="description"]', { name: 'description', content: description });
  upsertLink('canonical', url);

  upsertMeta('meta[property="og:title"]', { property: 'og:title', content: title });
  upsertMeta('meta[property="og:description"]', {
    property: 'og:description',
    content: description,
  });
  upsertMeta('meta[property="og:url"]', { property: 'og:url', content: url });
  upsertMeta('meta[property="og:image"]', { property: 'og:image', content: image });

  upsertMeta('meta[name="twitter:title"]', { name: 'twitter:title', content: title });
  upsertMeta('meta[name="twitter:description"]', {
    name: 'twitter:description',
    content: description,
  });
  upsertMeta('meta[name="twitter:image"]', { name: 'twitter:image', content: image });
}

export function PageSeo({ pathname }) {
  React.useEffect(() => {
    applyPageSeo(pathname);
  }, [pathname]);
  return null;
}

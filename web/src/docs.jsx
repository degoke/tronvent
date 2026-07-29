import React from 'react';
import {
  ArrowUpRight,
  ChevronRight,
  CircleHelp,
  Copy,
  HeartPulse,
  Menu,
  Server,
  Terminal,
} from 'lucide-react';
import { Link, Navigate, NavLink, Route, Routes, useLocation } from 'react-router-dom';
import { PageSeo } from './seo.js';
import './docs.css';
import './docs-routes.css';
import './docs-extra.css';

const LOGO_SRC = '/tronvent-logo.svg';

const join = (lines) => lines.join('\n');
const samples = {
  compose: join([
    'services:',
    '  postgres:',
    '    image: postgres:16-alpine',
    '    environment:',
    '      POSTGRES_DB: tronvent',
    '      POSTGRES_USER: tronvent',
    '      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-tronvent-local}',
    '    volumes:',
    '      - tronvent-postgres:/var/lib/postgresql/data',
    '    healthcheck:',
    '      test: ["CMD-SHELL", "pg_isready -U tronvent -d tronvent"]',
    '      interval: 5s',
    '      timeout: 5s',
    '      retries: 10',
    '',
    '  migrate:',
    '    image: ghcr.io/degoke/tronvent:1.0.0',
    '    command: ["/app/migrate"]',
    '    environment:',
    '      DATABASE_URL: postgres://tronvent:${POSTGRES_PASSWORD:-tronvent-local}@postgres:5432/tronvent',
    '    depends_on:',
    '      postgres:',
    '        condition: service_healthy',
    '',
    '  tronvent:',
    '    image: ghcr.io/degoke/tronvent:1.0.0',
    '    ports:',
    '      - "8080:8080"',
    '    env_file: .env',
    '    environment:',
    '      DATABASE_URL: postgres://tronvent:${POSTGRES_PASSWORD:-tronvent-local}@postgres:5432/tronvent',
    '    depends_on:',
    '      migrate:',
    '        condition: service_completed_successfully',
    '',
    'volumes:',
    '  tronvent-postgres:',
  ]),
  env: join([
    'TRONGRID_API_KEY_SCANNER=your-trongrid-api-key',
    'TRONGRID_BASE_URL=https://api.trongrid.io',
    'ADMIN_API_TOKEN=a-long-random-secret',
    'WEBHOOK_URL=https://your-app.example.com/webhooks/tron',
    'WEBHOOK_SIGNING_SECRET=another-long-random-secret',
  ]),
  helm: join([
    'helm registry login ghcr.io',
    'helm upgrade --install tronvent \\',
    '  oci://ghcr.io/degoke/charts/tronvent \\',
    '  --version 1.0.0 \\',
    '  --namespace tronvent --create-namespace \\',
    '  --set secrets.databaseUrl="$DATABASE_URL" \\',
    '  --set secrets.tronGridApiKey="$TRONGRID_API_KEY_SCANNER" \\',
    '  --set secrets.adminApiToken="$ADMIN_API_TOKEN" \\',
    '  --set secrets.webhookSigningSecret="$WEBHOOK_SIGNING_SECRET" \\',
    '  --set config.webhookUrl="$WEBHOOK_URL"',
  ]),
  watch: join([
    'BASE=http://localhost:8080',
    'AUTH="Authorization: Bearer $ADMIN_API_TOKEN"',
    '',
    'curl -s -X POST "$BASE/api/v1/addresses" \\',
    '  -H "$AUTH" -H "Content-Type: application/json" \\',
    '  -d \'{"address":"TXYZ..."}\'',
    '',
    'curl -s -X POST "$BASE/api/v1/contracts" \\',
    '  -H "$AUTH" -H "Content-Type: application/json" \\',
    '  -d \'{"contractAddress":"TR7NHqje...","tokenSymbol":"USDT"}\'',
  ]),
  webhook: join([
    'curl -s -X PUT "$BASE/api/v1/webhook" \\',
    '  -H "$AUTH" -H "Content-Type: application/json" \\',
    '  -d \'{"webhookUrl":"https://your-app.example.com/webhooks/tron","signingSecret":"another-long-random-secret"}\'',
  ]),
  body: join([
    '{',
    '  "id": "550e8400-e29b-41d4-a716-446655440000",',
    '  "type": "TRC20",',
    '  "txHash": "abc123...",',
    '  "fromAddress": "TXyz...",',
    '  "toAddress": "TAbc...",',
    '  "amount": "1000000",',
    '  "tokenContractAddress": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",',
    '  "blockNumber": 65432100,',
    '  "blockTimestamp": 1719234567000,',
    '  "confirmations": 5',
    '}',
  ]),
};

function DocsLogo() {
  return <img className="docs-logo-img" src={LOGO_SRC} alt="Tronvent" />;
}
function DocsHeader() {
  return (
    <header className="docs-header">
      <Link className="docs-brand" to="/">
        <DocsLogo />
      </Link>
      <div className="docs-header-links">
        <Link to="/">Website</Link>
        <a href="https://github.com/degoke/tronvent" target="_blank" rel="noopener noreferrer">
          GitHub <ArrowUpRight size={14} />
        </a>
      </div>
      <button className="docs-menu">
        <Menu size={20} />
      </button>
    </header>
  );
}
function CodeBlock({ children, label = 'TERMINAL' }) {
  return (
    <div className="docs-code">
      <div className="code-top">
        <span>{label}</span>
        <button aria-label="Copy code">
          <Copy size={14} />
        </button>
      </div>
      <pre>
        <code>{children}</code>
      </pre>
    </div>
  );
}
function Callout({ type = 'info', children }) {
  return (
    <div className={'callout ' + type}>
      <CircleHelp size={17} />
      <div>{children}</div>
    </div>
  );
}
function Page({ number, eyebrow, title, description, children }) {
  return (
    <article className="manual-page">
      <div className="manual-label">
        <span>{number}</span>
        {eyebrow}
      </div>
      <h2>{title}</h2>
      {description && <p className="manual-lede">{description}</p>}
      {children}
    </article>
  );
}
const links = [
  ['start', 'Getting started'],
  ['docker', 'Docker'],
  ['kubernetes', 'Kubernetes / Helm'],
  ['configure', 'Configuration'],
  ['dashboard', 'Dashboard & API'],
  ['watch', 'Watch activity'],
  ['webhooks', 'Webhooks'],
];
export default function DocsLayout() {
  const location = useLocation();
  return (
    <div className="docs-site">
      <PageSeo pathname={location.pathname} />
      <DocsHeader />
      <div className="docs-shell">
        <aside className="docs-sidebar">
          <div className="sidebar-title">
            <span className="eyebrow">DOCUMENTATION</span>
            <h1>Tronvent docs</h1>
            <p>Set up Tronvent and start receiving TRON notifications.</p>
          </div>
          <nav>
            {links.map(([slug, label]) => (
              <NavLink
                key={slug}
                to={'/docs/' + slug}
                className={({ isActive }) => (isActive ? 'current' : '')}
              >
                {label}
              </NavLink>
            ))}
          </nav>
        </aside>
        <main className="manual">
          <div className="manual-crumb">
            DOCS <ChevronRight size={13} />{' '}
            {links.find(([slug]) => location.pathname.endsWith(slug))?.[1]?.toUpperCase()}
          </div>
          <Routes>
            <Route path="start" element={<Start />} />
            <Route path="docker" element={<Docker />} />
            <Route path="kubernetes" element={<Kubernetes />} />
            <Route path="configure" element={<Configure />} />
            <Route path="dashboard" element={<Dashboard />} />
            <Route path="watch" element={<Watch />} />
            <Route path="webhooks" element={<Webhooks />} />
            <Route path="compose" element={<Navigate to="/docs/docker" replace />} />
            <Route path="*" element={<Navigate to="start" replace />} />
          </Routes>
        </main>
      </div>
      <footer className="docs-footer">
        <Link className="docs-brand" to="/">
          <DocsLogo />
        </Link>
        <span>Reliable TRON notifications for your application.</span>
        <a href="https://github.com/degoke/tronvent" target="_blank" rel="noopener noreferrer">
          Open source on GitHub <ArrowUpRight size={14} />
        </a>
      </footer>
    </div>
  );
}
function Start() {
  return (
    <Page
      number="01"
      eyebrow="GETTING STARTED"
      title="Get Tronvent running."
      description="Tronvent watches your wallets and tokens on TRON and notifies your application when something relevant happens. Deploy with Docker or Helm, add your settings, register what to watch, and check the dashboard to confirm notifications are arriving."
    >
      <div className="quick-links">
        <Link to="/docs/docker">
          <strong>Docker</strong>
          <span>
            Run the image or use Compose <ArrowUpRight size={14} />
          </span>
        </Link>
        <Link to="/docs/kubernetes">
          <strong>Kubernetes / Helm</strong>
          <span>
            Install with Helm <ArrowUpRight size={14} />
          </span>
        </Link>
      </div>
      <p>
        After deployment, go to <Link to="/docs/configure">Configuration</Link>, then{' '}
        <Link to="/docs/watch">Watch activity</Link> and <Link to="/docs/webhooks">Webhooks</Link>{' '}
        to add wallets, tokens, and your notification endpoint.
      </p>
    </Page>
  );
}
function Docker() {
  return (
    <Page
      number="02"
      eyebrow="DOCKER"
      title="Run with Docker."
      description="Docker is the easiest way to run Tronvent on a single server. Use the image alone if you already have a database, or Compose to run everything together on one machine."
    >
      <h3 className="subheading">Direct Docker image</h3>
      <p>
        Pull the image, run the database setup step, then start Tronvent on a network that can reach
        your database.
      </p>
      <CodeBlock label="pull">{'docker pull ghcr.io/degoke/tronvent:1.0.0'}</CodeBlock>
      <CodeBlock label="migrate">
        {
          'docker run --rm --network host \\\n  --env-file .env \\\n  -e DATABASE_URL="$DATABASE_URL" \\\n  ghcr.io/degoke/tronvent:1.0.0 /app/migrate'
        }
      </CodeBlock>
      <Callout type="warning">
        The network-host example is for local development. In production, run the migration and app
        containers on the same private network as your database.
      </Callout>
      <CodeBlock label="start">
        {
          'docker run --rm -p 8080:8080 \\\n  --env-file .env \\\n  -e DATABASE_URL="$DATABASE_URL" \\\n  ghcr.io/degoke/tronvent:1.0.0'
        }
      </CodeBlock>
      <h3 className="subheading">Docker Compose</h3>
      <p>
        Copy this <code>docker-compose.yml</code> and <code>.env</code> file, then start the stack.
        Compose waits for the database to be ready and runs migrations before Tronvent starts.
      </p>
      <CodeBlock label="docker-compose.yml">{samples.compose}</CodeBlock>
      <CodeBlock label=".env">{samples.env}</CodeBlock>
      <CodeBlock label="start">
        {
          'TRONVENT_IMAGE=ghcr.io/degoke/tronvent:1.0.0 docker compose up -d\ndocker compose logs -f tronvent'
        }
      </CodeBlock>
      <p>
        Open <code>http://localhost:8080</code> once the service is healthy. Stop with{' '}
        <code>docker compose down</code>. Add <code>-v</code> only if you want to delete local
        database data.
      </p>
    </Page>
  );
}
function Kubernetes() {
  return (
    <Page
      number="03"
      eyebrow="KUBERNETES / HELM"
      title="Run on Kubernetes with Helm."
      description="Helm charts are available on GHCR. Install once and configure secrets, networking, health checks, and monitoring through chart values."
    >
      <CodeBlock label="install">{samples.helm}</CodeBlock>
      <p>Check that everything is running and reach the service while testing:</p>
      <CodeBlock label="inspect">
        {
          'kubectl get pods -n tronvent\nkubectl describe deployment -n tronvent tronvent\nkubectl port-forward service/tronvent 8080:8080 -n tronvent'
        }
      </CodeBlock>
      <p>
        For production, store credentials in a Kubernetes Secret and enable ingress through chart
        values instead of passing secrets on the command line.
      </p>
    </Page>
  );
}
function Configure() {
  return (
    <Page
      number="04"
      eyebrow="CONFIGURATION"
      title="Set your configuration."
      description="These are the settings Tronvent needs to connect to TRON, store data, and send notifications. Start with the required values below."
    >
      <div className="config-table">
        <div>
          <code>DATABASE_URL</code>
          <span>Where Tronvent stores watches, progress, and pending notifications.</span>
        </div>
        <div>
          <code>TRONGRID_API_KEY_SCANNER</code>
          <span>Your TronGrid API key. Required on mainnet.</span>
        </div>
        <div>
          <code>TRONGRID_BASE_URL</code>
          <span>Which TRON network to use: mainnet, Shasta, or Nile.</span>
        </div>
        <div>
          <code>ADMIN_API_TOKEN</code>
          <span>Password for the dashboard and admin API.</span>
        </div>
        <div>
          <code>WEBHOOK_URL</code>
          <span>Where Tronvent sends transfer notifications.</span>
        </div>
        <div>
          <code>WEBHOOK_SIGNING_SECRET</code>
          <span>Secret used to sign each notification so your app can verify it.</span>
        </div>
      </div>
      <Callout>
        Tronvent waits for a set number of block confirmations before acting. More confirmations
        mean safer results; fewer mean faster notifications.
      </Callout>
    </Page>
  );
}
function Dashboard() {
  return (
    <Page
      number="05"
      eyebrow="DASHBOARD & API"
      title="Use the dashboard and API."
      description="Open your Tronvent URL, sign in with your admin token, and see what is being watched, what has been detected, and whether notifications were delivered. The API exposes the same information for scripts."
    >
      <div className="action-grid">
        <div>
          <Terminal size={18} />
          <strong>Dashboard</strong>
          <span>See watches, recent activity, and delivery status in one place.</span>
        </div>
        <div>
          <Server size={18} />
          <strong>Runtime API</strong>
          <span>
            <code>GET /api/v1/runtime</code> returns current status, counts, and active watches.
          </span>
        </div>
        <div>
          <HeartPulse size={18} />
          <strong>Health</strong>
          <span>
            <code>GET /health</code> for uptime checks; <code>GET /metrics</code> for monitoring
            tools.
          </span>
        </div>
      </div>
      <CodeBlock label="inspect">
        {
          'BASE=http://localhost:8080\nAUTH="Authorization: Bearer $ADMIN_API_TOKEN"\ncurl -s "$BASE/api/v1/runtime" -H "$AUTH" | jq\ncurl -s "$BASE/api/v1/addresses" -H "$AUTH" | jq'
        }
      </CodeBlock>
    </Page>
  );
}
function Watch() {
  return (
    <Page
      number="06"
      eyebrow="WATCH ACTIVITY"
      title="Add wallets and tokens to watch."
      description="Add wallet addresses to watch for TRX transfers and token contracts for token transfers. New watches take effect without restarting the service."
    >
      <CodeBlock label="add watches">{samples.watch}</CodeBlock>
      <p>
        Remove a watch with <code>DELETE /api/v1/addresses/&lt;address&gt;</code> or{' '}
        <code>DELETE /api/v1/contracts/&lt;contractAddress&gt;</code>. Only watch addresses and
        contracts your product actually uses.
      </p>
    </Page>
  );
}
function Webhooks() {
  return (
    <Page
      number="07"
      eyebrow="WEBHOOKS"
      title="Receive notifications in your app."
      description="When Tronvent finds a matching transfer, it sends a JSON notification to your app. Each message is signed so you can verify it, retried if delivery fails, and logged for review."
    >
      <h3 className="subheading">Set your notification URL</h3>
      <CodeBlock label="admin API">{samples.webhook}</CodeBlock>
      <h3 className="subheading">Request headers</h3>
      <div className="header-list">
        <div>
          <code>X-Tronvent-Event-Id</code>
          <span>
            Unique ID for this notification. Use it to avoid processing the same event twice.
          </span>
        </div>
        <div>
          <code>X-Tronvent-Event-Type</code>
          <span>
            Either <code>TRX</code> or <code>TRC20</code>.
          </span>
        </div>
        <div>
          <code>X-Tronvent-Timestamp</code>
          <span>When the notification was sent. Included in the signature.</span>
        </div>
        <div>
          <code>X-Tronvent-Signature</code>
          <span>Signature your app can verify with your signing secret.</span>
        </div>
        <div>
          <code>Content-Type</code>
          <span>
            <code>application/json</code>.
          </span>
        </div>
      </div>
      <h3 className="subheading">Example notification body</h3>
      <CodeBlock label="TRC20 event">{samples.body}</CodeBlock>
      <p>
        For token transfers, <code>amount</code> is the raw token amount — check the contract for
        decimals. For TRX, <code>amount</code> is in TRX units. Verify the signature before trusting
        the data, reject old timestamps, and return a success response only after your app has saved
        the event.
      </p>
      <Callout type="success">
        Handle duplicate notifications gracefully. Tronvent may send the same event more than once
        if a delivery is retried.
      </Callout>
    </Page>
  );
}

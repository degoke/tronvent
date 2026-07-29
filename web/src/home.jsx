import React from 'react';
import {
  ArrowUpRight,
  ChevronRight,
  Database,
  Github,
  LockKeyhole,
  Menu,
  Radio,
  ShieldCheck,
  Star,
  Zap,
} from 'lucide-react';
import { PageSeo } from './seo.js';
import './styles.css';

const GITHUB_REPO = 'https://github.com/degoke/tronvent';
const LOGO_SRC = '/tronvent-logo.svg';
const ICON_SRC = '/tronvent-icon.svg';

function Logo() {
  return (
    <a className="logo" href="/">
      <img className="logo-img" src={LOGO_SRC} alt="Tronvent" />
    </a>
  );
}
function GitHubStarButton() {
  const [stars, setStars] = React.useState(null);
  React.useEffect(() => {
    fetch('https://api.github.com/repos/degoke/tronvent')
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => {
        if (d) setStars(d.stargazers_count);
      })
      .catch(() => {});
  }, []);
  return (
    <a className="github-star-btn" href={GITHUB_REPO} target="_blank" rel="noopener noreferrer">
      <span className="github-star-label">
        <Github size={14} /> Star
      </span>
      {stars != null && (
        <span className="github-star-count">
          <Star size={11} />
          {stars}
        </span>
      )}
    </a>
  );
}
function Header() {
  return (
    <header className="site-header">
      <Logo />
      <div className="header-right">
        <nav>
          <a href="/docs/start">Docs</a>
        </nav>
        <GitHubStarButton />
        <a className="header-cta" href="/docs/start">
          Get started <ChevronRight size={15} />
        </a>
      </div>
      <button className="menu">
        <Menu size={20} />
      </button>
    </header>
  );
}
function Status({ children }) {
  return (
    <span className="status">
      <span className="status-dot" />
      {children}
    </span>
  );
}
function Pipeline() {
  const nodes = [
    [Radio, 'TRON network', 'block 65,432,108', 'live'],
    [Zap, 'Tronvent', '14 transfers found', 'live'],
    [Database, 'Saved', 'ready to send', 'saved'],
    [ShieldCheck, 'Your application', 'notified in 240ms', 'done'],
  ];
  return (
    <div className="pipeline-card">
      <div className="pipeline-head">
        <span className="eyebrow">THE FLOW</span>
        <Status>ACTIVE</Status>
      </div>
      <div className="pipeline-line" />
      {nodes.map(([Icon, label, detail, state], i) => (
        <div className="pipeline-node" key={label}>
          <div className={'node-icon ' + state}>
            <Icon size={18} />
          </div>
          <div>
            <strong>{label}</strong>
            <span>{detail}</span>
          </div>
          {i < 3 && (
            <div className="route-arrow">
              <ChevronRight size={16} />
            </div>
          )}
        </div>
      ))}
      <div className="pipeline-foot">
        <span>
          <span className="signal green" />
          99.98% delivered successfully
        </span>
        <span className="mono">updated 2s ago</span>
      </div>
    </div>
  );
}
function Feature({ icon: Icon, title, text }) {
  return (
    <article className="feature">
      <div className="feature-icon">
        <Icon size={20} />
      </div>
      <h3>{title}</h3>
      <p>{text}</p>
      <a href="/docs/start">
        Read the docs <ArrowUpRight size={14} />
      </a>
    </article>
  );
}
function Footer() {
  return (
    <footer>
      <div className="footer-top">
        <Logo />
        <p>
          Dependable notifications
          <br />
          for the TRON network.
        </p>
        <div className="footer-links">
          <a href="/docs/start">
            Documentation <ArrowUpRight size={14} />
          </a>
          <a href="https://github.com/degoke/tronvent">
            GitHub <ArrowUpRight size={14} />
          </a>
        </div>
      </div>
      <div className="footer-bottom">
        <span>© 2026 Tronvent. Open source.</span>
      </div>
    </footer>
  );
}
export default function Home() {
  return (
    <div className="app">
      <PageSeo pathname="/" />
      <Header />
      <main>
        <section className="hero">
          <div className="hero-grid" />
          <div className="hero-copy">
            <h1>
              Reliable transaction
              <br />
              <em>events</em> for TRON.
            </h1>
            <p className="hero-lede">
              Watch the wallets and tokens you care about. When a deposit, withdrawal, or transfer
              happens, Tronvent tells your application on infrastructure you control, without
              running your own node.
            </p>
            <div className="hero-actions">
              <a className="button primary" href="/docs/start">
                Get started <ArrowUpRight size={16} />
              </a>
              <a className="button ghost" href="/docs/start">
                See how it works <ChevronRight size={16} />
              </a>
            </div>
          </div>
          <div className="hero-art">
            <div className="art-label mono">TRONVENT / LIVE STATUS</div>
            <div className="art-blocks">
              {Array.from({ length: 45 }, (_, i) => (
                <span key={i} className={i % 11 === 0 || i === 27 ? 'hot' : ''} />
              ))}
            </div>
            <div className="art-route">
              <span />
              <span />
              <span />
              <span />
            </div>
            <div className="art-output">
              <img className="art-mark" src={ICON_SRC} alt="" aria-hidden="true" />
              <span className="output-core" />
            </div>
            <div className="art-caption">
              <span className="signal" />
              watching the network <span className="mono">every block</span>
            </div>
          </div>
        </section>
        <section className="proof">
          <div>
            <strong>3s</strong>
            <span>checks every 3 seconds</span>
          </div>
          <div>
            <strong>1M+</strong>
            <span>wallets supported</span>
          </div>
          <div>
            <strong>Signed</strong>
            <span>verified notifications</span>
          </div>
          <div>
            <strong>24/7</strong>
            <span>always running</span>
          </div>
        </section>
        <section className="section product" id="product">
          <div className="section-intro">
            <span className="eyebrow red">WHY TRONVENT</span>
            <h2>Know when money moves on TRON.</h2>
            <p>
              Your application needs timely notice of deposits, withdrawals, and transfers. Tronvent
              watches the wallets and tokens you choose and sends that activity straight to your
              backend.
            </p>
          </div>
          <div className="feature-grid">
            <Feature
              icon={Radio}
              title="Watch what matters"
              text="Add the addresses and tokens you care about. Tronvent monitors the network and surfaces only relevant transfers."
            />
            <Feature
              icon={Database}
              title="Always in sync"
              text="Restarts do not reset progress. Tronvent remembers what it has already processed and continues from there."
            />
            <Feature
              icon={LockKeyhole}
              title="Reliable delivery"
              text="Notifications are verified, retried if your app is temporarily down, and recorded so you can audit what was sent."
            />
          </div>
        </section>
        <section className="section flow" id="how-it-works">
          <div className="flow-heading">
            <span className="eyebrow red">HOW IT WORKS</span>
            <h2>
              From the network
              <br />
              to your app.
            </h2>
            <p>One clear path for TRX and token transfers.</p>
          </div>
          <Pipeline />
        </section>
        <section className="section use-cases">
          <div className="eyebrow red">WHO IT'S FOR</div>
          <div className="case-row">
            <div>
              <h2>Teams that move money on TRON.</h2>
            </div>
            <div className="cases">
              <span>Exchanges</span>
              <span>Wallets & apps</span>
              <span>Deposit systems</span>
              <span>Fintech backends</span>
            </div>
          </div>
        </section>
        <section className="cta">
          <div className="cta-grid" />
          <div className="eyebrow">READY WHEN YOU ARE</div>
          <h2>
            Start getting reliable
            <br />
            <em>TRON notifications.</em>
          </h2>
          <p>
            Run Tronvent alongside your app and know when activity happens — before it becomes a
            surprise.
          </p>
          <a className="button primary" href="/docs/start">
            Get started <ArrowUpRight size={16} />
          </a>
        </section>
      </main>
      <Footer />
    </div>
  );
}

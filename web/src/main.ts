/** SPA router: / -> landing, /s/:slug -> join+game, /s/:slug/edit -> map editor. */
import './styles.css';
import { h } from './ui/dom';

let cleanup: (() => void) | null = null;

export function navigate(path: string): void {
  window.history.pushState({}, '', path);
  window.dispatchEvent(new PopStateEvent('popstate'));
}

async function route(): Promise<void> {
  try {
    cleanup?.();
  } catch {
    /* ignore */
  }
  cleanup = null;
  const app = document.getElementById('app')!;
  app.innerHTML = '';
  window.scrollTo(0, 0);

  const path = window.location.pathname;
  let m: RegExpMatchArray | null;
  if (path === '/' || path === '') {
    const { renderLanding } = await import('./pages/landing');
    cleanup = renderLanding(app);
  } else if ((m = path.match(/^\/s\/([^/]+)\/edit\/?$/))) {
    const { renderEditor } = await import('./pages/editor');
    cleanup = await renderEditor(app, decodeURIComponent(m[1]));
  } else if ((m = path.match(/^\/s\/([^/]+)\/?$/))) {
    const { renderJoinPage } = await import('./pages/game/join');
    await renderJoinPage(app, decodeURIComponent(m[1]), async (ctx) => {
      const { startGame } = await import('./pages/game/client');
      app.innerHTML = '';
      cleanup = await startGame(app, ctx);
    });
  } else {
    app.appendChild(
      h('div', { class: 'join-page' },
        h('div', { class: 'join-card' },
          h('h2', { text: 'Nothing here.' }),
          h('p', { class: 'muted', text: 'That page does not exist.' }),
          h('a', { class: 'btn btn-primary', href: '/', text: 'Back home' }),
        ),
      ),
    );
  }
}

window.addEventListener('popstate', () => void route());
void route();

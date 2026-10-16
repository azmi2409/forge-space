/** Tiny typed DOM helpers — no framework, no dependencies. */

type AttrValue = string | number | boolean | null | undefined | EventListener;

export interface Attrs {
  [key: string]: AttrValue;
  class?: string;
  id?: string;
  text?: string;
}

/**
 * h('div', { class: 'x', onclick: fn }, 'text', childEl)
 * `text` attr sets textContent. on* attrs become listeners.
 */
export function h<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Attrs = {},
  ...children: (Node | string | number | null | undefined | false)[]
): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) continue;
    if (k === 'text') {
      el.textContent = String(v);
    } else if (k === 'class') {
      el.className = String(v);
    } else if (k.startsWith('on') && typeof v === 'function') {
      el.addEventListener(k.slice(2), v as EventListener);
    } else if (typeof v === 'boolean') {
      if (v) el.setAttribute(k, '');
    } else {
      el.setAttribute(k, String(v));
    }
  }
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

export function clear(el: HTMLElement): void {
  while (el.firstChild) el.removeChild(el.firstChild);
}

let toastBox: HTMLElement | null = null;

function ensureToastBox(): HTMLElement {
  if (!toastBox) {
    toastBox = h('div', { id: 'fs-toasts', 'aria-live': 'polite' });
    document.body.appendChild(toastBox);
  }
  return toastBox;
}

export type ToastKind = 'info' | 'ok' | 'err';

/** Non-blocking toast, auto-dismisses after 4s. */
export function toast(msg: string, kind: ToastKind = 'info'): void {
  const box = ensureToastBox();
  const el = h('div', { class: `fs-toast fs-toast-${kind}`, text: msg });
  box.appendChild(el);
  requestAnimationFrame(() => el.classList.add('show'));
  window.setTimeout(() => {
    el.classList.remove('show');
    window.setTimeout(() => el.remove(), 300);
  }, 4000);
}

/**
 * Modal dialog. Returns a close() function. Esc closes (unless opts.lock).
 * Only one modal at a time — opening a new one closes the old.
 */
export function openModal(title: string, body: HTMLElement, opts: { lock?: boolean; wide?: boolean } = {}): () => void {
  closeModal();
  const overlay = h('div', { class: 'fs-modal-overlay' });
  const card = h('div', { class: `fs-modal${opts.wide ? ' wide' : ''}` });
  const head = h('div', { class: 'fs-modal-head' }, h('h3', { text: title }));
  if (!opts.lock) {
    const x = h('button', { class: 'fs-icon-btn', text: '✕', 'aria-label': 'Close', onclick: () => close() });
    head.appendChild(x);
  }
  card.append(head, body);
  overlay.appendChild(card);
  document.body.appendChild(overlay);
  requestAnimationFrame(() => overlay.classList.add('show'));

  const onKey = (e: KeyboardEvent): void => {
    if (e.key === 'Escape' && !opts.lock) close();
  };
  document.addEventListener('keydown', onKey);

  function close(): void {
    document.removeEventListener('keydown', onKey);
    overlay.classList.remove('show');
    window.setTimeout(() => overlay.remove(), 200);
    if (currentOverlay === overlay) currentOverlay = null;
  }
  overlay.addEventListener('mousedown', (e) => {
    if (e.target === overlay && !opts.lock) close();
  });
  currentOverlay = overlay;
  return close;
}

let currentOverlay: HTMLElement | null = null;

export function closeModal(): void {
  currentOverlay?.remove();
  currentOverlay = null;
}

export function isModalOpen(): boolean {
  return currentOverlay !== null;
}

/** "HH:MM" from an RFC3339 timestamp, in the user's locale. */
export function fmtTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

export function debounce<F extends (...args: never[]) => void>(fn: F, ms: number): F {
  let t: number | null = null;
  return ((...args: never[]): void => {
    if (t !== null) window.clearTimeout(t);
    t = window.setTimeout(() => fn(...args), ms);
  }) as F;
}

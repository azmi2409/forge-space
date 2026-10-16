/** Shared sign-in / register modal. Resolves true when the user is authenticated. */
import { h, openModal, toast } from '../ui/dom';
import { ApiClient, loadUser, saveUser } from '../net/api';

export function openAuthModal(api: ApiClient, title = 'Sign in'): Promise<boolean> {
  return new Promise((resolve) => {
    if (loadUser()) {
      resolve(true);
      return;
    }
    let mode: 'login' | 'register' = 'login';
    let done = false;
    let obs: MutationObserver;
    const finish = (ok: boolean): void => {
      if (done) return;
      done = true;
      obs.disconnect();
      close();
      resolve(ok);
    };

    const email = h('input', { class: 'input', type: 'email', placeholder: 'you@example.com', autocomplete: 'email' }) as HTMLInputElement;
    const password = h('input', { class: 'input', type: 'password', placeholder: 'Password (min 8 chars)', autocomplete: 'current-password' }) as HTMLInputElement;
    const name = h('input', { class: 'input', type: 'text', placeholder: 'Display name', autocomplete: 'nickname' }) as HTMLInputElement;
    const err = h('p', { class: 'form-err' });
    const submitBtn = h('button', { class: 'btn btn-primary btn-block', text: 'Sign in' });

    const nameRow = h('label', { class: 'field hidden' }, 'Display name', name);
    const switchLink = h('button', { class: 'link', type: 'button' });

    const render = (): void => {
      nameRow.classList.toggle('hidden', mode === 'login');
      submitBtn.textContent = mode === 'login' ? 'Sign in' : 'Create account';
      switchLink.textContent = mode === 'login' ? 'New here? Create an account' : 'Already have an account? Sign in';
    };
    switchLink.onclick = () => {
      mode = mode === 'login' ? 'register' : 'login';
      err.textContent = '';
      render();
    };

    const submit = async (): Promise<void> => {
      err.textContent = '';
      const em = email.value.trim();
      const pw = password.value;
      if (!em || !pw) {
        err.textContent = 'Email and password are required.';
        return;
      }
      submitBtn.textContent = '…';
      (submitBtn as HTMLButtonElement).disabled = true;
      try {
        if (mode === 'login') {
          const { user } = await api.login(em, pw);
          saveUser(user);
        } else {
          const nm = name.value.trim();
          if (!nm) {
            err.textContent = 'Pick a display name.';
            return;
          }
          await api.register(em, pw, nm);
          const { user } = await api.login(em, pw);
          saveUser(user);
        }
        toast('Signed in. Welcome in.', 'ok');
        finish(true);
      } catch (e) {
        err.textContent = e instanceof Error ? e.message : 'Sign-in failed';
      } finally {
        (submitBtn as HTMLButtonElement).disabled = false;
        render();
      }
    };
    submitBtn.onclick = () => void submit();
    for (const input of [email, password, name]) {
      input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') void submit();
      });
    }

    render();
    const body = h(
      'div',
      { class: 'auth-form' },
      h('label', { class: 'field' }, 'Email', email),
      h('label', { class: 'field' }, 'Password', password),
      nameRow,
      err,
      submitBtn,
      h('p', { class: 'center' }, switchLink),
    );
    const close = openModal(title, body);
    // if the user closes the modal manually, resolve false
    obs = new MutationObserver(() => {
      if (!document.body.contains(body) && !done) {
        done = true;
        obs.disconnect();
        resolve(false);
      }
    });
    obs.observe(document.body, { childList: true, subtree: true });
  });
}

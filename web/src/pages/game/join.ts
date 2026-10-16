/**
 * Join flow for /s/:slug — auth gate, display name, avatar customizer
 * (skin×5, hair×6, hair color×6, top×8, live canvas preview), device setup
 * (mic/camera/speaker selects + mic test meter), Join button.
 */
import { h, toast } from '../../ui/dom';
import { ApiClient, ApiError, loadUser, saveUser } from '../../net/api';
import {
  drawAvatar,
  SKIN_COLORS,
  HAIR_COLORS,
  TOP_COLORS,
  HAIR_NAMES,
  TOP_NAMES,
} from '../../game/render';
import type { AvatarSpec, JoinResult, Space } from '../../game/types';
import { openAuthModal } from '../auth';

const AVATAR_KEY = 'forge-space:avatar';
const NAME_KEY = 'forge-space:name';
const DEVICES_KEY = 'forge-space:devices';

export interface DevicePrefs {
  micId: string;
  camId: string;
  speakerId: string;
  micOn: boolean;
  camOn: boolean;
}

export function loadAvatar(): AvatarSpec {
  try {
    const raw = localStorage.getItem(AVATAR_KEY);
    if (raw) {
      const a = JSON.parse(raw) as AvatarSpec;
      return {
        skin: a.skin % 5,
        hair: a.hair % 6,
        hair_color: a.hair_color % 6,
        top: a.top % 8,
      };
    }
  } catch {
    /* fall through */
  }
  return { skin: 0, hair: 1, hair_color: 0, top: 0 };
}

export function saveAvatar(a: AvatarSpec): void {
  localStorage.setItem(AVATAR_KEY, JSON.stringify(a));
}

export function loadDevicePrefs(): DevicePrefs {
  try {
    const raw = localStorage.getItem(DEVICES_KEY);
    if (raw) return JSON.parse(raw) as DevicePrefs;
  } catch {
    /* fall through */
  }
  return { micId: '', camId: '', speakerId: '', micOn: true, camOn: false };
}

export function saveDevicePrefs(d: DevicePrefs): void {
  localStorage.setItem(DEVICES_KEY, JSON.stringify(d));
}

export interface JoinContext {
  api: ApiClient;
  space: Space;
  join: JoinResult;
  devices: DevicePrefs;
}

export async function renderJoinPage(app: HTMLElement, slug: string, onJoined: (ctx: JoinContext) => void): Promise<void> {
  const api = new ApiClient();
  const wrap = h('div', { class: 'join-page' });
  app.appendChild(wrap);

  let space: Space | null = null;
  try {
    space = await api.spaceBySlug(slug);
  } catch (e) {
    wrap.appendChild(errorCard('Could not reach the server.', e instanceof Error ? e.message : ''));
    return;
  }
  if (!space) {
    wrap.appendChild(
      errorCard(`No space called “${slug}” exists.`, 'Check the link, or head home and create your own space.'),
    );
    return;
  }

  const render = (): void => {
    while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
    const user = loadUser();
    if (!user) {
      wrap.appendChild(authGate(space!, () => void signIn()));
    } else {
      wrap.appendChild(joinForm(space!, user.display_name, onJoined));
    }
  };

  const signIn = async (): Promise<void> => {
    const ok = await openAuthModal(api, `Join ${space!.name}`);
    if (ok) render();
  };

  const signOut = (): void => {
    api.logout();
    saveUser(null);
    render();
  };

  // expose sign-out via a tiny header on the form
  (wrap as HTMLElement & { _signOut?: () => void })._signOut = signOut;
  render();

  function errorCard(title: string, body: string): HTMLElement {
    return h(
      'div',
      { class: 'join-card' },
      h('h2', { text: title }),
      h('p', { class: 'muted', text: body }),
      h('a', { class: 'btn btn-primary', href: '/', text: 'Back home' }),
    );
  }

  function authGate(sp: Space, onSignIn: () => void): HTMLElement {
    return h(
      'div',
      { class: 'join-card' },
      h('p', { class: 'eyebrow', text: 'You are joining' }),
      h('h2', { text: sp.name }),
      h('p', { class: 'muted', text: 'Sign in to walk in. One account, thirty seconds — then pick a face and go.' }),
      h('button', { class: 'btn btn-primary btn-lg btn-block', text: 'Sign in / create account', onclick: onSignIn }),
      h('p', {}, h('a', { class: 'link', href: '/', text: '← Back home' })),
    );
  }

  function joinForm(sp: Space, displayName: string, onJoinedCb: (ctx: JoinContext) => void): HTMLElement {
    const avatar = loadAvatar();
    const devices = loadDevicePrefs();
    const storedName = localStorage.getItem(NAME_KEY);
    const nameInput = h('input', {
      class: 'input input-lg',
      type: 'text',
      maxlength: '30',
      placeholder: 'Display name',
      value: storedName ?? displayName,
    }) as HTMLInputElement;

    // --- avatar customizer ---
    const preview = h('canvas', { class: 'avatar-preview', width: '120', height: '132' }) as HTMLCanvasElement;
    const drawPreview = (): void => {
      const ctx = preview.getContext('2d')!;
      ctx.clearRect(0, 0, preview.width, preview.height);
      drawAvatar(ctx, {
        look: avatar,
        x: 60,
        y: 118,
        dir: 'down',
        emote: '',
        ghost: false,
        speaking: false,
        isAgent: false,
        name: '',
        now: performance.now(),
        scale: 2.2,
        showName: false,
      });
    };

    const optionRow = (current: number, onPick: (i: number) => void, renderBtn: (b: HTMLButtonElement, i: number) => void, labels: string[]): HTMLElement => {
      const row = h('div', { class: 'swatches' });
      labels.forEach((label, i) => {
        const b = h('button', {
          class: `swatch${i === current ? ' active' : ''}`,
          title: label,
          'aria-label': label,
          onclick: () => {
            onPick(i);
            [...row.children].forEach((el, j) => el.classList.toggle('active', j === i));
            drawPreview();
            saveAvatar(avatar);
          },
        }) as HTMLButtonElement;
        renderBtn(b, i);
        row.appendChild(b);
      });
      return row;
    };

    const swatchRow = (
      colors: string[],
      current: number,
      onPick: (i: number) => void,
      label: (i: number) => string,
    ): HTMLElement =>
      optionRow(current, onPick, (b, i) => {
        b.style.background = colors[i];
      }, colors.map((_, i) => label(i)));

    const avatarBox = h(
      'div',
      { class: 'avatar-box' },
      preview,
      h(
        'div',
        { class: 'avatar-opts' },
        h('p', { class: 'opt-label', text: 'Skin' }),
        swatchRow(SKIN_COLORS, avatar.skin, (i) => (avatar.skin = i), (i) => `Skin ${i + 1}`),
        h('p', { class: 'opt-label', text: 'Hair' }),
        optionRow(avatar.hair, (i) => (avatar.hair = i), (b, i) => {
          b.classList.add('text-swatch');
          b.textContent = HAIR_NAMES[i];
        }, HAIR_NAMES),
        h('p', { class: 'opt-label', text: 'Hair color' }),
        swatchRow(HAIR_COLORS, avatar.hair_color, (i) => (avatar.hair_color = i), (i) => `Hair color ${i + 1}`),
        h('p', { class: 'opt-label', text: 'Top' }),
        swatchRow(TOP_COLORS, avatar.top, (i) => (avatar.top = i), (i) => TOP_NAMES[i]),
      ),
    );

    // --- device setup ---
    const micSel = h('select', { class: 'input' }) as HTMLSelectElement;
    const camSel = h('select', { class: 'input' }) as HTMLSelectElement;
    const spkSel = h('select', { class: 'input' }) as HTMLSelectElement;
    const micOn = h('input', { type: 'checkbox', checked: devices.micOn ? '' : null }) as HTMLInputElement;
    const camOn = h('input', { type: 'checkbox', checked: devices.camOn ? '' : null }) as HTMLInputElement;

    const fillSelect = (sel: HTMLSelectElement, infos: MediaDeviceInfo[], current: string, noneLabel: string): void => {
      sel.innerHTML = '';
      sel.appendChild(new Option(noneLabel, ''));
      for (const info of infos) {
        sel.appendChild(new Option(info.label || `${info.kind} ${sel.length}`, info.deviceId));
      }
      sel.value = current;
    };

    const refreshDevices = async (): Promise<void> => {
      try {
        const all = await navigator.mediaDevices.enumerateDevices();
        fillSelect(micSel, all.filter((d) => d.kind === 'audioinput'), devices.micId, 'Default microphone');
        fillSelect(camSel, all.filter((d) => d.kind === 'videoinput'), devices.camId, 'Default camera');
        fillSelect(spkSel, all.filter((d) => d.kind === 'audiooutput'), devices.speakerId, 'Default speaker');
      } catch {
        /* devices unavailable — selects stay at defaults */
      }
    };
    void refreshDevices();

    // mic test meter
    const meter = h('canvas', { class: 'meter', width: '220', height: '14' }) as HTMLCanvasElement;
    const meterBtn = h('button', { class: 'btn btn-ghost btn-sm', text: 'Test mic' });
    let meterStop: (() => void) | null = null;
    const stopMeter = (): void => {
      meterStop?.();
      meterStop = null;
      meterBtn.textContent = 'Test mic';
      const ctx = meter.getContext('2d')!;
      ctx.clearRect(0, 0, meter.width, meter.height);
    };
    meterBtn.onclick = async () => {
      if (meterStop) {
        stopMeter();
        return;
      }
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: devices.micId ? { deviceId: devices.micId } : true });
        // also refresh labels now that we have permission
        void refreshDevices();
        const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
        const ac = new Ctx();
        const src = ac.createMediaStreamSource(stream);
        const an = ac.createAnalyser();
        an.fftSize = 256;
        src.connect(an);
        const buf = new Float32Array(an.fftSize);
        const ctx = meter.getContext('2d')!;
        let raf = 0;
        const tick = (): void => {
          an.getFloatTimeDomainData(buf);
          let sum = 0;
          for (let i = 0; i < buf.length; i++) sum += buf[i] * buf[i];
          const level = Math.min(1, Math.sqrt(sum / buf.length) * 4);
          ctx.clearRect(0, 0, meter.width, meter.height);
          ctx.fillStyle = '#10b981';
          ctx.fillRect(0, 0, meter.width * level, meter.height);
          raf = requestAnimationFrame(tick);
        };
        tick();
        meterBtn.textContent = 'Stop test';
        meterStop = () => {
          cancelAnimationFrame(raf);
          stream.getTracks().forEach((t) => t.stop());
          void ac.close();
        };
      } catch {
        toast('Could not access the microphone.', 'err');
      }
    };

    const deviceBox = h(
      'div',
      { class: 'device-box' },
      h('label', { class: 'check' }, micOn, h('span', { text: 'Microphone' })),
      micSel,
      h('div', { class: 'meter-row' }, meter, meterBtn),
      h('label', { class: 'check' }, camOn, h('span', { text: 'Camera' })),
      camSel,
      h('label', { class: 'field-label', text: 'Speaker' }),
      spkSel,
    );

    const joinBtn = h('button', { class: 'btn btn-primary btn-lg btn-block', text: `Join ${sp.name}` });
    const errP = h('p', { class: 'form-err' });

    joinBtn.onclick = () => void doJoin();

    async function doJoin(): Promise<void> {
      const name = nameInput.value.trim().slice(0, 30);
      if (!name) {
        errP.textContent = 'Pick a display name first.';
        return;
      }
      devices.micId = micSel.value;
      devices.camId = camSel.value;
      devices.speakerId = spkSel.value;
      devices.micOn = micOn.checked;
      devices.camOn = camOn.checked;
      saveDevicePrefs(devices);
      saveAvatar(avatar);
      localStorage.setItem(NAME_KEY, name);
      stopMeter();

      joinBtn.textContent = 'Joining…';
      (joinBtn as HTMLButtonElement).disabled = true;
      try {
        const join = await api.join(sp.id, name, avatar);
        onJoinedCb({ api, space: sp, join, devices });
      } catch (e) {
        (joinBtn as HTMLButtonElement).disabled = false;
        joinBtn.textContent = `Join ${sp.name}`;
        if (e instanceof ApiError && e.status === 401) {
          errP.textContent = 'Session expired — please sign in again.';
          api.logout();
          saveUser(null);
          render();
        } else {
          errP.textContent = e instanceof Error ? e.message : 'Join failed';
        }
      }
    }

    const card = h(
      'div',
      { class: 'join-card join-form' },
      h(
        'div',
        { class: 'join-head' },
        h('div', {}, h('p', { class: 'eyebrow', text: 'You are joining' }), h('h2', { text: sp.name })),
        h('button', { class: 'btn btn-ghost btn-sm', text: 'Sign out', onclick: () => (wrap as unknown as { _signOut: () => void })._signOut() }),
      ),
      h('label', { class: 'field' }, 'Display name', nameInput),
      h('p', { class: 'opt-label', text: 'Avatar' }),
      avatarBox,
      h('p', { class: 'opt-label', text: 'Devices' }),
      deviceBox,
      errP,
      joinBtn,
      h('p', { class: 'hint center', text: 'WASD or arrows to move · X to interact · Z to dance · G for ghost mode' }),
    );
    drawPreview();
    return card;
  }
}

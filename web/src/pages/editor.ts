/**
 * Map editor for /s/:slug/edit (owner only).
 * Tile paint (floor/wall/erase), drag-rect -> private/spotlight area, object
 * palette with props panel, spawn setter, Ctrl+Z undo for paint, Save -> PUT map.
 */
import { h, clear, toast } from '../ui/dom';
import { ApiClient, loadUser } from '../net/api';
import { TILE_PX, createTileset, drawMap } from '../game/render';
import { Tile } from '../game/engine';
import type { Area, GameMap, MapSummary, ObjectKind, Space, SpaceObject } from '../game/types';
import { openAuthModal } from './auth';

type Tool = 'paint' | 'rect' | 'object' | 'spawn';

const OBJECT_KINDS: { kind: ObjectKind; icon: string; label: string }[] = [
  { kind: 'note', icon: '📝', label: 'Note' },
  { kind: 'whiteboard', icon: '🧑‍🏫', label: 'Whiteboard' },
  { kind: 'url', icon: '🔗', label: 'Link' },
  { kind: 'video', icon: '🎬', label: 'Video' },
  { kind: 'poster', icon: '🖼️', label: 'Poster' },
  { kind: 'portal', icon: '🌀', label: 'Portal' },
];

interface PaintOp {
  changes: { i: number; before: number }[];
}

export async function renderEditor(app: HTMLElement, slug: string): Promise<() => void> {
  const api = new ApiClient();
  const root = h('div', { class: 'editor' });
  app.appendChild(root);

  if (!loadUser()) {
    const ok = await openAuthModal(api, 'Map editor — sign in first');
    if (!ok) {
      window.location.href = `/s/${slug}`;
      return () => {};
    }
  }

  const space: Space | null = await api.spaceBySlug(slug).catch(() => null);
  if (!space) {
    root.appendChild(h('div', { class: 'join-card' }, h('h2', { text: `No space called “${slug}” exists.` }), h('a', { class: 'btn btn-primary', href: '/', text: 'Back home' })));
    return () => {};
  }
  const spc: Space = space;
  let meId = '';
  try {
    meId = JSON.parse(localStorage.getItem('forge-space:user') ?? '{}').id ?? '';
  } catch {
    meId = '';
  }
  if (meId !== space.owner_user_id) {
    root.appendChild(
      h('div', { class: 'join-card' },
        h('h2', { text: 'Only the space owner can edit this map.' }),
        h('p', { class: 'muted', text: 'Ask the owner for access, or create your own space.' }),
        h('a', { class: 'btn btn-primary', href: `/s/${slug}`, text: `Back to ${space.name}` }),
      ),
    );
    return () => {};
  }

  const { maps } = await api.getSpace(spc.id).catch(() => ({ maps: [] as MapSummary[] }));
  if (maps.length === 0) {
    root.appendChild(h('div', { class: 'join-card' }, h('h2', { text: 'This space has no maps yet.' })));
    return () => {};
  }

  // --- state ---
  let map: GameMap = (await api.getMap(space.id, maps[0].id)).map;
  let areas: Area[] = (await api.getAreas(spc.id).catch(() => ({ areas: [] }))).areas;
  let objects: SpaceObject[] = (await api.getObjects(spc.id).catch(() => ({ objects: [] }))).objects;
  let tool: Tool = 'paint';
  let brush: number = Tile.Floor;
  let objectKind: ObjectKind = 'note';
  let selectedObject: SpaceObject | null = null;
  let rectKind: 'private' | 'spotlight' = 'private';
  let rectName = '';
  const undoStack: PaintOp[] = [];

  const sprites = createTileset();
  const SCALE = 20 / TILE_PX; // 20px tiles while editing

  // --- top bar ---
  const saveBtn = h('button', { class: 'btn btn-primary', text: 'Save map' });
  const dirtyDot = h('span', { class: 'dirty-dot hidden', text: '● unsaved' });
  saveBtn.onclick = async () => {
    try {
      const res = await api.putMap(spc.id, map.id, { tiles: map.tiles, width: map.width, height: map.height, spawn: map.spawn });
      map = res.map;
      dirtyDot.classList.add('hidden');
      undoStack.length = 0;
      toast('Map published ✓', 'ok');
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Save failed', 'err');
    }
  };
  const undoBtn = h('button', { class: 'btn btn-ghost', text: 'Undo (Ctrl+Z)', disabled: '' });
  undoBtn.onclick = undo;
  const topbar = h(
    'div',
    { class: 'editor-topbar' },
    h('a', { class: 'btn btn-ghost btn-sm', href: `/s/${slug}`, text: '← Back to space' }),
    h('strong', { text: `${spc.name} — ${map.name}` }),
    h('span', { class: 'muted', text: `${map.width}×${map.height}` }),
    dirtyDot,
    h('span', { class: 'spacer' }),
    undoBtn,
    saveBtn,
  );

  // --- toolbar ---
  const toolBtn = (t: Tool, label: string): HTMLButtonElement => {
    const b = h('button', { class: `tool-btn${tool === t ? ' active' : ''}`, text: label, title: label }) as HTMLButtonElement;
    b.onclick = () => {
      tool = t;
      selectedObject = null;
      [...toolbar.querySelectorAll('.tool-btn')].forEach((el) => el.classList.toggle('active', el === b));
      hintEl.textContent = toolHint();
      renderProps();
      draw();
    };
    return b;
  };
  const toolbar = h(
    'div',
    { class: 'editor-toolbar' },
    toolBtn('paint', '🖌️ Paint'),
    toolBtn('rect', '▦ Area'),
    toolBtn('object', '🧩 Object'),
    toolBtn('spawn', '📍 Spawn'),
  );

  const brushRow = h('div', { class: 'brush-row' });
  const refreshBrushes = (): void => {
    clear(brushRow);
    for (const [value, label] of [[Tile.Floor, 'Floor'], [Tile.Wall, 'Wall'], [Tile.Void, 'Erase']] as [number, string][]) {
      const b = h('button', { class: `chip${brush === value ? ' active' : ''}`, text: label });
      b.onclick = () => {
        brush = value;
        refreshBrushes();
      };
      brushRow.appendChild(b);
    }
  };
  refreshBrushes();

  const kindSel = h('select', { class: 'input' }) as HTMLSelectElement;
  kindSel.appendChild(new Option('Private area', 'private'));
  kindSel.appendChild(new Option('Spotlight area', 'spotlight'));
  kindSel.onchange = () => (rectKind = kindSel.value as 'private' | 'spotlight');
  const nameInput = h('input', { class: 'input', placeholder: 'Area name (e.g. “Lounge”)' }) as HTMLInputElement;
  nameInput.oninput = () => (rectName = nameInput.value);
  const areaBox = h('div', { class: 'area-box' }, h('label', { class: 'field' }, 'Area kind', kindSel), h('label', { class: 'field' }, 'Name', nameInput));

  const paletteBox = h('div', { class: 'palette' });
  for (const { kind, icon, label } of OBJECT_KINDS) {
    const b = h('button', { class: `chip${objectKind === kind ? ' active' : ''}`, text: `${icon} ${label}` });
    b.onclick = () => {
      objectKind = kind;
      [...paletteBox.children].forEach((el, i) => el.classList.toggle('active', OBJECT_KINDS[i].kind === kind));
    };
    paletteBox.appendChild(b);
  }

  const hintEl = h('p', { class: 'hint' });
  const toolHint = (): string => {
    switch (tool) {
      case 'paint':
        return 'Click or drag to paint tiles.';
      case 'rect':
        return 'Drag a rectangle, then name it below to create a private/spotlight area.';
      case 'object':
        return 'Pick an object kind, then click the map to place it. Click an existing object to edit.';
      case 'spawn':
        return 'Click the map to move the spawn point.';
    }
  };
  hintEl.textContent = toolHint();

  // --- canvas ---
  const canvas = h('canvas', { class: 'editor-canvas' }) as HTMLCanvasElement;
  const ctx = canvas.getContext('2d')!;
  canvas.width = Math.floor(map.width * TILE_PX * SCALE);
  canvas.height = Math.floor(map.height * TILE_PX * SCALE);
  const canvasWrap = h('div', { class: 'editor-canvas-wrap' }, canvas);

  // --- props panel ---
  const propsEl = h('div', { class: 'editor-props' });
  const areasEl = h('div', { class: 'editor-list' });
  const objectsEl = h('div', { class: 'editor-list' });

  const renderProps = (): void => {
    clear(propsEl);
    if (tool === 'object' && selectedObject) {
      propsEl.appendChild(objectProps(selectedObject));
    } else {
      propsEl.appendChild(h('p', { class: 'muted', text: tool === 'object' ? 'Click the map to place a new object, or click an existing one to edit it.' : 'Select the Object tool and click an object to edit its properties.' }));
    }
  };

  const objectProps = (o: SpaceObject): HTMLElement => {
    const title = h('input', { class: 'input', value: o.title, placeholder: 'Title' }) as HTMLInputElement;
    const body = h('textarea', { class: 'input', placeholder: 'Body / notes', value: o.body }) as HTMLTextAreaElement;
    const url = h('input', { class: 'input', value: o.url ?? '', placeholder: 'https://… (url, video)' }) as HTMLInputElement;
    const wIn = h('input', { class: 'input', type: 'number', value: String(o.w), min: '1', max: '20' }) as HTMLInputElement;
    const hIn = h('input', { class: 'input', type: 'number', value: String(o.h), min: '1', max: '20' }) as HTMLInputElement;
    const solid = h('input', { type: 'checkbox', checked: o.solid ? '' : null }) as HTMLInputElement;
    const tMap = h('input', { class: 'input', value: o.target_map_id ?? '', placeholder: 'Target map id (portal)' }) as HTMLInputElement;
    const tX = h('input', { class: 'input', type: 'number', value: String(o.target_x ?? 0) }) as HTMLInputElement;
    const tY = h('input', { class: 'input', type: 'number', value: String(o.target_y ?? 0) }) as HTMLInputElement;

    const save = h('button', { class: 'btn btn-primary btn-sm', text: 'Save object' });
    save.onclick = async () => {
      try {
        const res = await api.patchObject(o.id, {
          title: title.value.trim(),
          body: body.value,
          url: url.value.trim() || undefined,
          w: Math.max(1, parseInt(wIn.value || '1', 10)),
          h: Math.max(1, parseInt(hIn.value || '1', 10)),
          solid: solid.checked,
          target_map_id: tMap.value.trim() || undefined,
          target_x: parseInt(tX.value || '0', 10),
          target_y: parseInt(tY.value || '0', 10),
        });
        Object.assign(o, res.object);
        toast('Object saved ✓', 'ok');
        renderLists();
        draw();
      } catch (e) {
        toast(e instanceof Error ? e.message : 'Save failed', 'err');
      }
    };
    const del = h('button', { class: 'btn btn-danger btn-sm', text: 'Delete' });
    del.onclick = async () => {
      if (!window.confirm(`Delete “${o.title || o.kind}”?`)) return;
      try {
        await api.deleteObject(o.id);
        objects = objects.filter((x) => x.id !== o.id);
        selectedObject = null;
        toast('Object deleted.', 'ok');
        renderProps();
        renderLists();
        draw();
      } catch (e) {
        toast(e instanceof Error ? e.message : 'Delete failed', 'err');
      }
    };
    return h(
      'div',
      { class: 'props-form' },
      h('h4', { text: `${OBJECT_KINDS.find((k) => k.kind === o.kind)?.icon ?? ''} ${o.kind} — properties` }),
      h('label', { class: 'field' }, 'Title', title),
      h('label', { class: 'field' }, 'Body', body),
      h('label', { class: 'field' }, 'URL', url),
      h('div', { class: 'row' }, h('label', { class: 'field' }, 'W', wIn), h('label', { class: 'field' }, 'H', hIn)),
      h('label', { class: 'check' }, solid, h('span', { text: 'Solid (blocks movement)' })),
      o.kind === 'portal' ? h('label', { class: 'field' }, 'Target map id', tMap) : null,
      o.kind === 'portal' ? h('div', { class: 'row' }, h('label', { class: 'field' }, 'Target X', tX), h('label', { class: 'field' }, 'Target Y', tY)) : null,
      h('div', { class: 'row' }, save, del),
    );
  };

  const renderLists = (): void => {
    clear(areasEl);
    areasEl.appendChild(h('h4', { text: `Areas (${areas.length})` }));
    for (const a of areas) {
      const row = h('div', { class: 'list-row' },
        h('span', { text: `${a.kind === 'private' ? '🔒' : '🔦'} ${a.name}` }),
        h('button', { class: 'btn btn-ghost btn-xs', text: 'Delete', onclick: () => void deleteArea(a) }),
      );
      areasEl.appendChild(row);
    }
    clear(objectsEl);
    objectsEl.appendChild(h('h4', { text: `Objects (${objects.length})` }));
    for (const o of objects) {
      const row = h('div', { class: `list-row${selectedObject?.id === o.id ? ' active' : ''}` },
        h('span', { text: `${OBJECT_KINDS.find((k) => k.kind === o.kind)?.icon ?? ''} ${o.title || o.kind}` }),
      );
      row.onclick = () => {
        selectedObject = o;
        tool = 'object';
        renderProps();
        renderLists();
        draw();
      };
      objectsEl.appendChild(row);
    }
  };

  async function deleteArea(a: Area): Promise<void> {
    if (!window.confirm(`Delete area “${a.name}”?`)) return;
    try {
      await api.deleteArea(a.id);
      areas = areas.filter((x) => x.id !== a.id);
      toast('Area deleted.', 'ok');
      renderLists();
      draw();
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Delete failed', 'err');
    }
  }

  // --- drawing ---
  let dragRect: { x0: number; y0: number; x1: number; y1: number } | null = null;
  let painting = false;
  let currentOp: PaintOp | null = null;
  let hover: { x: number; y: number } | null = null;

  function draw(): void {
    ctx.setTransform(SCALE, 0, 0, SCALE, 0, 0);
    ctx.fillStyle = '#0a0e16';
    ctx.fillRect(0, 0, map.width * TILE_PX, map.height * TILE_PX);
    drawMap(ctx, map, sprites, (map.width * TILE_PX) / 2, (map.height * TILE_PX) / 2, map.width * TILE_PX, map.height * TILE_PX);

    // grid
    ctx.strokeStyle = 'rgba(255,255,255,0.05)';
    ctx.lineWidth = 1 / SCALE;
    ctx.beginPath();
    for (let x = 0; x <= map.width; x++) {
      ctx.moveTo(x * TILE_PX, 0);
      ctx.lineTo(x * TILE_PX, map.height * TILE_PX);
    }
    for (let y = 0; y <= map.height; y++) {
      ctx.moveTo(0, y * TILE_PX);
      ctx.lineTo(map.width * TILE_PX, y * TILE_PX);
    }
    ctx.stroke();

    // areas
    for (const a of areas) {
      const color = a.kind === 'private' ? '16,185,129' : '245,158,11';
      ctx.fillStyle = `rgba(${color},0.18)`;
      ctx.fillRect(a.x * TILE_PX, a.y * TILE_PX, a.w * TILE_PX, a.h * TILE_PX);
      ctx.strokeStyle = `rgba(${color},0.9)`;
      ctx.lineWidth = 2 / SCALE;
      ctx.strokeRect(a.x * TILE_PX, a.y * TILE_PX, a.w * TILE_PX, a.h * TILE_PX);
    }

    // objects
    ctx.font = `${20 / SCALE}px sans-serif`;
    ctx.textAlign = 'center';
    for (const o of objects) {
      const cx = (o.x + o.w / 2) * TILE_PX;
      const cy = (o.y + o.h / 2) * TILE_PX;
      ctx.fillText(OBJECT_KINDS.find((k) => k.kind === o.kind)?.icon ?? '•', cx, cy + 7 / SCALE);
      if (selectedObject?.id === o.id) {
        ctx.strokeStyle = '#10b981';
        ctx.lineWidth = 2 / SCALE;
        ctx.strokeRect(o.x * TILE_PX, o.y * TILE_PX, o.w * TILE_PX, o.h * TILE_PX);
      }
    }

    // spawn
    ctx.strokeStyle = '#10b981';
    ctx.lineWidth = 2 / SCALE;
    ctx.beginPath();
    ctx.arc((map.spawn.x + 0.5) * TILE_PX, (map.spawn.y + 0.5) * TILE_PX, 14 / SCALE, 0, Math.PI * 2);
    ctx.stroke();

    // drag rect preview
    if (dragRect) {
      const x = Math.min(dragRect.x0, dragRect.x1);
      const y = Math.min(dragRect.y0, dragRect.y1);
      const w = Math.abs(dragRect.x1 - dragRect.x0) + 1;
      const hh = Math.abs(dragRect.y1 - dragRect.y0) + 1;
      const color = rectKind === 'private' ? '16,185,129' : '245,158,11';
      ctx.fillStyle = `rgba(${color},0.3)`;
      ctx.fillRect(x * TILE_PX, y * TILE_PX, w * TILE_PX, hh * TILE_PX);
    }

    // hover tile
    if (hover && tool === 'paint') {
      ctx.strokeStyle = 'rgba(255,255,255,0.6)';
      ctx.lineWidth = 2 / SCALE;
      ctx.strokeRect(hover.x * TILE_PX + 1, hover.y * TILE_PX + 1, TILE_PX - 2, TILE_PX - 2);
    }
  }

  // --- mouse ---
  const toTileSimple = (e: MouseEvent): { x: number; y: number } => {
    const rect = canvas.getBoundingClientRect();
    const px = ((e.clientX - rect.left) / rect.width) * canvas.width;
    const py = ((e.clientY - rect.top) / rect.height) * canvas.height;
    return {
      x: Math.max(0, Math.min(map.width - 1, Math.floor(px / SCALE / TILE_PX))),
      y: Math.max(0, Math.min(map.height - 1, Math.floor(py / SCALE / TILE_PX))),
    };
  };

  const paintAt = (tx: number, ty: number): void => {
    const i = ty * map.width + tx;
    if (map.tiles[i] === brush) return;
    if (!currentOp) currentOp = { changes: [] };
    if (!currentOp.changes.some((c) => c.i === i)) {
      currentOp.changes.push({ i, before: map.tiles[i] });
    }
    map.tiles[i] = brush;
    dirtyDot.classList.remove('hidden');
  };

  canvas.addEventListener('mousedown', (e) => {
    const { x, y } = toTileSimple(e);
    if (tool === 'paint') {
      painting = true;
      currentOp = null;
      paintAt(x, y);
      draw();
    } else if (tool === 'rect') {
      dragRect = { x0: x, y0: y, x1: x, y1: y };
    } else if (tool === 'spawn') {
      map.spawn = { x, y };
      dirtyDot.classList.remove('hidden');
      toast(`Spawn moved to (${x}, ${y}) — save to publish.`, 'info');
      draw();
    } else if (tool === 'object') {
      // select existing object under cursor?
      const hit = objects.find((o) => x >= o.x && x < o.x + o.w && y >= o.y && y < o.y + o.h);
      if (hit) {
        selectedObject = hit;
        renderProps();
        renderLists();
        draw();
      } else {
        void placeObject(x, y);
      }
    }
  });

  canvas.addEventListener('mousemove', (e) => {
    const { x, y } = toTileSimple(e);
    hover = { x, y };
    if (tool === 'paint' && painting) {
      paintAt(x, y);
      draw();
    } else if (tool === 'rect' && dragRect) {
      dragRect.x1 = x;
      dragRect.y1 = y;
      draw();
    } else if (tool === 'paint') {
      draw();
    }
  });

  const onMouseUp = (): void => {
    if (painting && currentOp && currentOp.changes.length > 0) {
      undoStack.push(currentOp);
      (undoBtn as HTMLButtonElement).disabled = false;
    }
    painting = false;
    currentOp = null;
    if (dragRect) {
      const r = dragRect;
      dragRect = null;
      void finishRect(r);
    }
    draw();
  };
  window.addEventListener('mouseup', onMouseUp);

  async function placeObject(x: number, y: number): Promise<void> {
    try {
      const res = await api.createObject(spc.id, {
        map_id: map.id,
        kind: objectKind,
        x,
        y,
        w: 1,
        h: 1,
        title: '',
        body: '',
        solid: false,
      });
      objects.push(res.object);
      selectedObject = res.object;
      toast(`${OBJECT_KINDS.find((k) => k.kind === objectKind)?.label} placed — edit its properties on the right.`, 'ok');
      renderProps();
      renderLists();
      draw();
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Place failed', 'err');
    }
  }

  async function finishRect(r: { x0: number; y0: number; x1: number; y1: number }): Promise<void> {
    const x = Math.min(r.x0, r.x1);
    const y = Math.min(r.y0, r.y1);
    const w = Math.abs(r.x1 - r.x0) + 1;
    const h = Math.abs(r.y1 - r.y0) + 1;
    const name = rectName.trim() || `${rectKind === 'private' ? 'Private' : 'Spotlight'} area`;
    try {
      const res = await api.createArea(spc.id, { map_id: map.id, kind: rectKind, name, x, y, w, h });
      areas.push(res.area);
      toast(`Area “${name}” created ✓`, 'ok');
      renderLists();
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Area creation failed', 'err');
    }
  }

  function undo(): void {
    const op = undoStack.pop();
    if (!op) return;
    for (const c of op.changes) map.tiles[c.i] = c.before;
    (undoBtn as HTMLButtonElement).disabled = undoStack.length === 0;
    draw();
  }

  const onKeyDown = (e: KeyboardEvent): void => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z') {
      e.preventDefault();
      undo();
    }
  };
  window.addEventListener('keydown', onKeyDown);

  // --- layout ---
  const sidebar = h(
    'div',
    { class: 'editor-sidebar' },
    h('h4', { text: 'Brush' }),
    brushRow,
    h('h4', { text: 'New area (drag on map)' }),
    areaBox,
    h('h4', { text: 'Object palette' }),
    paletteBox,
    hintEl,
    h('h4', { text: 'Properties' }),
    propsEl,
    areasEl,
    objectsEl,
  );
  root.append(topbar, h('div', { class: 'editor-main' }, toolbar, canvasWrap, sidebar));

  renderProps();
  renderLists();
  draw();

  return () => {
    window.removeEventListener('mouseup', onMouseUp);
    window.removeEventListener('keydown', onKeyDown);
  };
}

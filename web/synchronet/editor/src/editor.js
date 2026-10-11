/**
 * AnsiEditor — Embeddable ANSI art editor (Moebius-inspired).
 *
 * Usage:
 *   const editor = await AnsiEditor.create({
 *       container: document.getElementById('editor'),
 *       columns: 80,
 *       rows: 25,
 *       fontUrl: './fonts/IBM VGA.F16',
 *       onDone: (ansiBytes) => { ... },
 *       onCancel: () => { ... },
 *   });
 */

import { ega, rgbString } from './lib/palette.js';
import { charToCp437, cp437ToChar } from './lib/encodings.js';
import { Font } from './lib/font.js';
import { TextDocument } from './lib/document.js';
import { Renderer } from './lib/renderer.js';
import { encodeAsAnsi } from './lib/ansi.js';
import { detectFileType, getContentBytes, parseSauce, loadAnsi, loadBin } from './lib/sauce.js';
import { bresenhamLine, rectOutline, rectFilled, ellipseOutline, ellipseFilled, ellipseFromDrag } from './lib/shapes.js';
import cssText from './editor.css';

// ─── Constants ───

const TOOLS = ['select', 'brush', 'line', 'rect', 'ellipse', 'fill', 'sample'];
const TOOL_ICONS = {
    // Marquee / selection (dashed rectangle)
    select: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="2 2"><rect x="2" y="2" width="12" height="12" rx="1"/></svg>',
    // Paintbrush
    brush: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 2l4 4-7 7H3v-4l7-7z"/><path d="M8.5 3.5l4 4"/></svg>',
    // Diagonal line
    line: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><line x1="3" y1="13" x2="13" y2="3"/><circle cx="3" cy="13" r="1.2" fill="currentColor"/><circle cx="13" cy="3" r="1.2" fill="currentColor"/></svg>',
    // Rectangle
    rect: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="3" width="12" height="10" rx="1"/></svg>',
    // Ellipse
    ellipse: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><ellipse cx="8" cy="8" rx="6" ry="5"/></svg>',
    // Paint bucket / fill
    fill: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3"><path d="M3.5 9.5l4-8 4 8c0 2.2-1.8 4-4 4s-4-1.8-4-4z"/><path d="M3.5 9.5h8" stroke-dasharray="1 1.5"/><circle cx="13.5" cy="11" r="1.5" fill="currentColor" stroke="none"/></svg>',
    // Eyedropper / color picker
    sample: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M13 1.5l1.5 1.5-2 2-1.5-1.5z"/><path d="M11.5 3.5L5 10l-1 3 3-1 6.5-6.5"/><path d="M4 12l-1.5 1.5"/></svg>',
};
const TOOL_TIPS = {
    select:  'Select Mode (Alt+K)',  brush:   'Brush Mode (Alt+B)',
    line:    'Line Tool (Alt+L)',      rect:    'Rectangle Tool (Alt+R)',
    ellipse: 'Ellipse Tool (Alt+E)',   fill:    'Fill Mode (Alt+F)',
    sample:  'Sample Mode (Alt+S)',
};
const SHAPE_TOOLS = ['line', 'rect', 'ellipse'];
const BRUSH_MODES = ['half_block', 'custom_block', 'shading'];
const BRUSH_MODE_LABELS = { half_block: 'Half', custom_block: 'Char', shading: 'Shade' };

const FKEY_SETS = [
    [218, 191, 192, 217, 196, 179, 195, 180, 193, 194, 32, 32],
    [201, 187, 200, 188, 205, 186, 204, 185, 202, 203, 32, 32],
    [213, 184, 212, 190, 205, 179, 198, 181, 207, 209, 32, 32],
    [214, 183, 211, 189, 196, 186, 199, 182, 208, 210, 32, 32],
    [197, 206, 216, 215, 176, 177, 178, 219, 220, 223, 32, 32],
    [176, 177, 178, 219, 223, 220, 221, 222, 254, 250, 32, 32],
];

// ─── Main Class ───

export class AnsiEditor {
    static async create(options) {
        const editor = new AnsiEditor(options);
        await editor.init();
        return editor;
    }

    constructor(options) {
        this.container = options.container;
        this.columns   = options.columns || 80;
        this.rows      = options.rows || 25;
        this.onDone    = options.onDone || (() => {});
        this.onCancel  = options.onCancel || (() => {});
        this.fontUrl   = options.fontUrl || './fonts/IBM VGA.F16';
        this.iceColors = options.iceColors || false;

        this.fg = 7;
        this.bg = 0;
        this.cursorX = 0;
        this.cursorY = 0;
        this.activeTool = 'select';
        this.brushMode = 'half_block';
        this.customBlockChar = 219;
        this.insertMode = false;
        this.fkeySetIndex = 0;
        this._mouseDown = false;
        this._mouseButton = 0;
        this._lastMouseX = -1;
        this._lastMouseY = -1;
        this._lastMouseHalfY = -1;

        // Shape tool state
        this.shapeFilled = false;
        this._shapeStartX = -1;
        this._shapeStartY = -1;
        this._shapeOverlay = null;

        this.font = null;
        this.doc = null;
        this.renderer = null;
        this.el = {};
        this._handlers = {};
    }

    async init() {
        if (!document.getElementById('me-editor-styles')) {
            const style = document.createElement('style');
            style.id = 'me-editor-styles';
            style.textContent = cssText;
            document.head.appendChild(style);
        }

        this.font = new Font();
        await this.font.load({ url: this.fontUrl, palette: ega });

        this.doc = new TextDocument(this.columns, this.rows);
        this.renderer = new Renderer(this.doc, this.font);
        this.renderer.render();

        this._buildDOM();
        this._fullRender();
        this._updateCursor();
        this._updatePalette();
        this._updateStatusBar();
        this._updateToolbar();
        this._selectTool('select');
        this._setupInput();
        this._updatePreview();
        this.el.root.focus();
    }

    // ═══ DOM BUILDING ═══

    _buildDOM() {
        const root = document.createElement('div');
        root.className = 'me-editor';
        root.tabIndex = 0;

        const body = document.createElement('div');
        body.className = 'me-body';

        const sidebar = this._buildSidebar();
        const main = document.createElement('div');
        main.className = 'me-main';

        const toolbar = this._buildToolbar();
        const viewport = this._buildViewport();
        const statusbar = this._buildStatusBar();
        main.append(toolbar, viewport, statusbar);

        const preview = this._buildPreview();
        body.append(sidebar, main, preview);

        const actionBar = document.createElement('div');
        actionBar.className = 'me-action-bar';

        const actionLeft = document.createElement('div');
        actionLeft.className = 'me-action-left';
        const btnLoad = document.createElement('button');
        btnLoad.className = 'me-btn';
        btnLoad.textContent = 'Load File';
        btnLoad.onclick = () => this._promptLoadFile();
        actionLeft.appendChild(btnLoad);

        const actionRight = document.createElement('div');
        actionRight.className = 'me-action-right';
        const btnCancel = document.createElement('button');
        btnCancel.className = 'me-btn';
        btnCancel.textContent = 'Cancel';
        btnCancel.onclick = () => this.cancel();
        const btnDone = document.createElement('button');
        btnDone.className = 'me-btn me-btn-done';
        btnDone.textContent = 'Done';
        btnDone.onclick = () => this.done();
        actionRight.append(btnCancel, btnDone);
        actionBar.append(actionLeft, actionRight);

        const backdrop = document.createElement('div');
        backdrop.className = 'me-backdrop';
        backdrop.onclick = () => this._hideOverlay();
        const overlay = this._buildAttributeOverlay();
        const resizeOverlay = this._buildResizeOverlay();

        root.append(body, actionBar, backdrop, overlay, resizeOverlay);
        this.el.root = root;
        this.el.backdrop = backdrop;
        this.container.innerHTML = '';
        this.container.appendChild(root);
    }

    _buildSidebar() {
        const sidebar = document.createElement('div');
        sidebar.className = 'me-sidebar';

        const colors = document.createElement('div');
        colors.className = 'me-current-colors';
        const fgSwatch = document.createElement('div');
        fgSwatch.className = 'me-color-swatch me-color-fg';
        fgSwatch.title = 'Foreground color';
        fgSwatch.onclick = () => this._showAttributeOverlay('fg');
        const bgSwatch = document.createElement('div');
        bgSwatch.className = 'me-color-swatch me-color-bg';
        bgSwatch.title = 'Background color';
        bgSwatch.onclick = () => this._showAttributeOverlay('bg');
        colors.append(bgSwatch, fgSwatch);
        this.el.fgSwatch = fgSwatch;
        this.el.bgSwatch = bgSwatch;

        const palette = document.createElement('div');
        palette.className = 'me-palette';
        this.el.palCells = [];
        // Moebius palette layout: 2 columns — dark (0-7) left, bright (8-15) right
        const palOrder = [0, 8, 1, 9, 2, 10, 3, 11, 4, 12, 5, 13, 6, 14, 7, 15];
        for (const ci of palOrder) {
            const cell = document.createElement('div');
            cell.className = 'me-pal-cell';
            cell.style.backgroundColor = rgbString(ega[ci]);
            cell.title = 'Color ' + ci;
            cell.dataset.colorIndex = ci;
            cell.addEventListener('mousedown', (e) => {
                e.preventDefault();
                if (e.button === 0) this._setFg(ci);
                else if (e.button === 2) this._setBg(ci);
            });
            cell.addEventListener('contextmenu', (e) => e.preventDefault());
            palette.appendChild(cell);
            this.el.palCells.push(cell);
        }

        const sep = document.createElement('div');
        sep.className = 'me-sep';

        const toolList = document.createElement('div');
        toolList.className = 'me-tool-list';
        this.el.toolBtns = {};
        for (const tool of TOOLS) {
            const btn = document.createElement('div');
            btn.className = 'me-tool-btn';
            btn.innerHTML = TOOL_ICONS[tool];
            btn.title = TOOL_TIPS[tool];
            btn.onclick = () => this._selectTool(tool);
            toolList.appendChild(btn);
            this.el.toolBtns[tool] = btn;
        }

        // Fill toggle (outline vs filled) for rect/ellipse
        const fillToggle = document.createElement('div');
        fillToggle.className = 'me-fill-toggle';
        const optOutline = document.createElement('div');
        optOutline.className = 'me-fill-opt me-active';
        optOutline.textContent = '▭';
        optOutline.title = 'Outline';
        optOutline.onclick = () => { this.shapeFilled = false; this._updateFillToggle(); };
        const optFilled = document.createElement('div');
        optFilled.className = 'me-fill-opt';
        optFilled.textContent = '■';
        optFilled.title = 'Filled';
        optFilled.onclick = () => { this.shapeFilled = true; this._updateFillToggle(); };
        fillToggle.append(optOutline, optFilled);
        this.el.fillToggle = fillToggle;
        this.el.fillOptOutline = optOutline;
        this.el.fillOptFilled = optFilled;

        sidebar.append(colors, palette, sep, toolList, fillToggle);
        return sidebar;
    }

    _buildToolbar() {
        const toolbar = document.createElement('div');
        toolbar.className = 'me-toolbar';

        const fkeyLeft = document.createElement('div');
        fkeyLeft.className = 'me-fkey-nav';
        fkeyLeft.textContent = '\u25C0';
        fkeyLeft.onclick = () => this._prevFkeySet();

        const fkeyGroup = document.createElement('div');
        fkeyGroup.className = 'me-fkey-group';
        this.el.fkeyCanvases = [];
        for (let i = 0; i < 12; i++) {
            const fk = document.createElement('div');
            fk.className = 'me-fkey';
            fk.title = 'F' + (i + 1);
            const canvas = document.createElement('canvas');
            canvas.width = this.font.width;
            canvas.height = this.font.height;
            canvas.style.width = (this.font.width * 2) + 'px';
            canvas.style.height = (this.font.height * 2) + 'px';
            const label = document.createElement('div');
            label.className = 'me-fkey-label';
            label.textContent = 'F' + (i + 1);
            fk.append(canvas, label);
            fk.onclick = () => this._typeFkey(i);
            fkeyGroup.appendChild(fk);
            this.el.fkeyCanvases.push(canvas);
        }

        const fkeyRight = document.createElement('div');
        fkeyRight.className = 'me-fkey-nav';
        fkeyRight.textContent = '\u25B6';
        fkeyRight.onclick = () => this._nextFkeySet();

        const sep1 = document.createElement('div');
        sep1.className = 'me-toolbar-sep';

        const brushModes = document.createElement('div');
        brushModes.className = 'me-brush-modes';
        this.el.brushModeBtns = {};
        for (const mode of BRUSH_MODES) {
            const btn = document.createElement('div');
            btn.className = 'me-brush-mode';
            btn.textContent = BRUSH_MODE_LABELS[mode];
            btn.onclick = () => this._setBrushMode(mode);
            brushModes.appendChild(btn);
            this.el.brushModeBtns[mode] = btn;
        }
        this.el.brushModes = brushModes;

        toolbar.append(fkeyLeft, fkeyGroup, fkeyRight, sep1, brushModes);
        this.el.toolbar = toolbar;
        return toolbar;
    }

    _buildViewport() {
        const viewport = document.createElement('div');
        viewport.className = 'me-viewport';

        const container = document.createElement('div');
        container.className = 'me-canvas-container';

        const canvas = this.renderer.canvas;
        canvas.className = 'me-canvas';
        canvas.style.width = this.renderer.width + 'px';
        canvas.style.height = this.renderer.height + 'px';

        const editingLayer = document.createElement('div');
        editingLayer.className = 'me-editing-layer';

        const cursorCanvas = document.createElement('canvas');
        cursorCanvas.className = 'me-cursor-canvas me-cursor-blink';
        cursorCanvas.width = this.font.width;
        cursorCanvas.height = this.font.height;
        cursorCanvas.style.width = this.font.width + 'px';
        cursorCanvas.style.height = this.font.height + 'px';

        const selectionOverlay = document.createElement('div');
        selectionOverlay.className = 'me-selection-overlay me-hidden';

        editingLayer.appendChild(cursorCanvas);
        editingLayer.appendChild(selectionOverlay);
        container.append(canvas, editingLayer);
        viewport.appendChild(container);

        this.el.viewport = viewport;
        this.el.canvasContainer = container;
        this.el.canvas = canvas;
        this.el.cursorCanvas = cursorCanvas;
        this.el.editingLayer = editingLayer;
        this.el.selectionOverlay = selectionOverlay;

        // Update preview on scroll
        viewport.addEventListener('scroll', () => this._updateViewFrame());

        return viewport;
    }

    _buildStatusBar() {
        const bar = document.createElement('div');
        bar.className = 'me-statusbar';
        const pos = document.createElement('span');
        this.el.statusPos = pos;
        const dim = document.createElement('span');
        dim.className = 'me-status-dim';
        dim.title = 'Click to resize canvas';
        dim.onclick = () => this._showResizeOverlay();
        this.el.statusDim = dim;
        const mode = document.createElement('span');
        this.el.statusMode = mode;
        const tool = document.createElement('span');
        this.el.statusTool = tool;
        bar.append(pos, dim, mode, tool);
        return bar;
    }

    _buildAttributeOverlay() {
        const overlay = document.createElement('div');
        overlay.className = 'me-attr-overlay';
        const title = document.createElement('h3');
        title.textContent = 'Select Color';
        this.el.attrTitle = title;
        const grid = document.createElement('div');
        grid.className = 'me-attr-grid';
        this.el.attrCells = [];
        for (let i = 0; i < 16; i++) {
            const cell = document.createElement('div');
            cell.className = 'me-attr-cell';
            cell.style.backgroundColor = rgbString(ega[i]);
            cell.onclick = () => this._onAttrPick(i);
            grid.appendChild(cell);
            this.el.attrCells.push(cell);
        }
        const actions = document.createElement('div');
        actions.className = 'me-attr-actions';
        const closeBtn = document.createElement('button');
        closeBtn.className = 'me-btn';
        closeBtn.textContent = 'Close';
        closeBtn.onclick = () => this._hideOverlay();
        actions.appendChild(closeBtn);
        overlay.append(title, grid, actions);
        this.el.attrOverlay = overlay;
        this._attrTarget = 'fg';
        return overlay;
    }

    // ─── Resize Overlay ───

    _buildResizeOverlay() {
        const overlay = document.createElement('div');
        overlay.className = 'me-resize-overlay';

        const title = document.createElement('h3');
        title.textContent = 'Set Canvas Size';

        const form = document.createElement('div');
        form.className = 'me-resize-form';

        const lblC = document.createElement('label');
        lblC.textContent = 'Columns:';
        const inpC = document.createElement('input');
        inpC.type = 'number'; inpC.min = '1'; inpC.max = '3000';
        inpC.className = 'me-resize-input';

        const lblR = document.createElement('label');
        lblR.textContent = 'Rows:';
        const inpR = document.createElement('input');
        inpR.type = 'number'; inpR.min = '1'; inpR.max = '10000';
        inpR.className = 'me-resize-input';

        form.append(lblC, inpC, lblR, inpR);

        const actions = document.createElement('div');
        actions.className = 'me-attr-actions';
        const btnOk = document.createElement('button');
        btnOk.className = 'me-btn me-btn-done';
        btnOk.textContent = 'Resize';
        btnOk.onclick = () => this._doResize(parseInt(inpC.value), parseInt(inpR.value));
        const btnCancel = document.createElement('button');
        btnCancel.className = 'me-btn';
        btnCancel.textContent = 'Cancel';
        btnCancel.onclick = () => this._hideOverlay();
        actions.append(btnOk, btnCancel);

        overlay.append(title, form, actions);
        this.el.resizeOverlay = overlay;
        this.el.resizeColsInput = inpC;
        this.el.resizeRowsInput = inpR;
        return overlay;
    }

    // ─── Mini Preview Panel ───

    _buildPreview() {
        const panel = document.createElement('div');
        panel.className = 'me-preview';

        const label = document.createElement('div');
        label.className = 'me-preview-label';
        label.textContent = 'Preview';

        const wrapper = document.createElement('div');
        wrapper.className = 'me-preview-wrapper';
        wrapper.style.position = 'relative';

        const canvas = document.createElement('canvas');
        canvas.className = 'me-preview-canvas';
        canvas.width = 1; canvas.height = 1;

        const viewFrame = document.createElement('div');
        viewFrame.className = 'me-view-frame';
        viewFrame.style.display = 'block';

        wrapper.append(canvas, viewFrame);
        panel.append(label, wrapper);

        this.el.preview = panel;
        this.el.previewWrapper = wrapper;
        this.el.previewCanvas = canvas;
        this.el.viewFrame = viewFrame;

        // Click preview to scroll
        wrapper.addEventListener('mousedown', (e) => this._handlePreviewMouse(e));
        wrapper.addEventListener('mousemove', (e) => { if (this._previewDrag) this._handlePreviewMouse(e); });
        document.addEventListener('mouseup', () => { this._previewDrag = false; });
        this._previewDrag = false;

        return panel;
    }

    _updatePreview() {
        const src = this.renderer.canvas;
        const dst = this.el.previewCanvas;
        if (!dst || !src) return;

        const maxW = 200;
        const scale = maxW / src.width;
        dst.width = Math.round(src.width * scale);
        dst.height = Math.round(src.height * scale);
        dst.style.width = dst.width + 'px';
        dst.style.height = dst.height + 'px';

        const ctx = dst.getContext('2d');
        ctx.imageSmoothingEnabled = false;
        ctx.drawImage(src, 0, 0, dst.width, dst.height);

        this._updateViewFrame();
    }

    _updateViewFrame() {
        const vp = this.el.viewport;
        const dst = this.el.previewCanvas;
        const vf = this.el.viewFrame;
        if (!vp || !dst || !vf) return;

        const src = this.renderer.canvas;
        const scale = dst.width / src.width;

        const frameW = Math.round(vp.clientWidth * scale);
        const frameH = Math.round(vp.clientHeight * scale);
        const frameX = Math.round(vp.scrollLeft * scale);
        const frameY = Math.round(vp.scrollTop * scale);

        vf.style.width = Math.min(frameW, dst.width) + 'px';
        vf.style.height = Math.min(frameH, dst.height) + 'px';
        vf.style.left = frameX + 'px';
        vf.style.top = frameY + 'px';
    }

    _handlePreviewMouse(e) {
        this._previewDrag = true;
        const dst = this.el.previewCanvas;
        const vp = this.el.viewport;
        const src = this.renderer.canvas;
        const rect = dst.getBoundingClientRect();
        const scale = src.width / dst.width;

        const px = (e.clientX - rect.left) * scale;
        const py = (e.clientY - rect.top) * scale;
        vp.scrollLeft = px - vp.clientWidth / 2;
        vp.scrollTop = py - vp.clientHeight / 2;
        this._updateViewFrame();
    }

    // ═══ INPUT ═══

    _setupInput() {
        this._handlers.keydown = (e) => this._handleKeyDown(e);
        this._handlers.mousedown = (e) => this._handleMouseDown(e);
        this._handlers.mousemove = (e) => this._handleMouseMove(e);
        this._handlers.mouseup = (e) => this._handleMouseUp(e);
        this._handlers.contextmenu = (e) => e.preventDefault();

        this.el.root.addEventListener('keydown', this._handlers.keydown);
        this.el.viewport.addEventListener('mousedown', this._handlers.mousedown);
        this.el.viewport.addEventListener('contextmenu', this._handlers.contextmenu);
        document.addEventListener('mousemove', this._handlers.mousemove);
        document.addEventListener('mouseup', this._handlers.mouseup);
    }

    _getCanvasXY(event) {
        const rect = this.el.canvas.getBoundingClientRect();
        const scaleX = this.renderer.width / rect.width;
        const scaleY = this.renderer.height / rect.height;
        const px = (event.clientX - rect.left) * scaleX;
        const py = (event.clientY - rect.top) * scaleY;
        const x = Math.floor(px / this.font.width);
        const y = Math.floor(py / this.font.height);
        const halfY = Math.floor(py / (this.font.height / 2));
        return { x, y, halfY };
    }

    // ── Keyboard ──

    _handleKeyDown(e) {
        const ctrl = e.ctrlKey || e.metaKey;

        if (ctrl && e.key === 'z') { e.preventDefault(); this._undo(); return; }
        if (ctrl && (e.key === 'y' || (e.shiftKey && e.key === 'Z'))) { e.preventDefault(); this._redo(); return; }

        // Clipboard: Ctrl+C / Ctrl+X / Ctrl+V
        if (ctrl && e.key === 'c' && this._selection) { e.preventDefault(); this._copySelection(); return; }
        if (ctrl && e.key === 'x' && this._selection) { e.preventDefault(); this._cutSelection(); return; }
        if (ctrl && e.key === 'v' && this._clipboard) { e.preventDefault(); this._pasteClipboard(); return; }
        if (ctrl && e.key === 'a' && this.activeTool === 'select') {
            e.preventDefault();
            this._selection = { sx: 0, sy: 0, dx: this.columns - 1, dy: this.rows - 1 };
            this._updateSelectionOverlay();
            return;
        }

        if (e.key === 'Escape') {
            e.preventDefault();
            // Cancel shape in progress
            if (this._shapeOverlay) { this._destroyOverlay(); return; }
            // Cancel selection
            if (this._selection) { this._clearSelection(); return; }
            if (this.el.attrOverlay.classList.contains('me-visible') ||
                this.el.resizeOverlay.classList.contains('me-visible')) {
                this._hideOverlay();
            } else {
                this._showAttributeOverlay('fg');
            }
            return;
        }

        // Delete erases selection
        if (e.key === 'Delete' && this._selection && this.activeTool === 'select') {
            e.preventDefault();
            this._eraseSelection();
            return;
        }

        // Alt+key tool shortcuts
        if (e.altKey && !ctrl) {
            switch (e.key.toLowerCase()) {
                case 'k': e.preventDefault(); this._selectTool('select');  return;
                case 'b': e.preventDefault(); this._selectTool('brush');   return;
                case 'l': e.preventDefault(); this._selectTool('line');    return;
                case 'r': e.preventDefault(); this._selectTool('rect');    return;
                case 'e': e.preventDefault(); this._selectTool('ellipse'); return;
                case 'f': e.preventDefault(); this._selectTool('fill');    return;
                case 's': e.preventDefault(); this._selectTool('sample');  return;
            }
        }

        var fmatch = e.key.match(/^F(\d+)$/);
        if (fmatch) {
            var num = parseInt(fmatch[1]) - 1;
            if (num >= 0 && num < 12) {
                e.preventDefault();
                this._typeFkey(num);
                return;
            }
        }

        if (this.activeTool === 'select') {
            this._handleSelectKey(e);
        }
    }

    _handleSelectKey(e) {
        var ctrl = e.ctrlKey || e.metaKey;
        switch (e.key) {
            case 'ArrowLeft':  e.preventDefault(); this._moveCursor(this.cursorX - 1, this.cursorY); break;
            case 'ArrowRight': e.preventDefault(); this._moveCursor(this.cursorX + 1, this.cursorY); break;
            case 'ArrowUp':    e.preventDefault(); this._moveCursor(this.cursorX, this.cursorY - 1); break;
            case 'ArrowDown':  e.preventDefault(); this._moveCursor(this.cursorX, this.cursorY + 1); break;
            case 'Home':       e.preventDefault(); this._moveCursor(0, this.cursorY); break;
            case 'End':        e.preventDefault(); this._moveCursor(this.columns - 1, this.cursorY); break;
            case 'PageUp':     e.preventDefault(); this._moveCursor(this.cursorX, Math.max(0, this.cursorY - 25)); break;
            case 'PageDown':   e.preventDefault(); this._moveCursor(this.cursorX, Math.min(this.rows - 1, this.cursorY + 25)); break;
            case 'Enter':
                e.preventDefault();
                // Auto-grow if at last row
                if (this.cursorY >= this.rows - 1) {
                    this.doc.growRows(1); this.rows = this.doc.rows;
                    this._rebuildCanvas();
                }
                this._moveCursor(0, this.cursorY + 1);
                break;
            case 'Backspace':  e.preventDefault(); this._backspace(); break;
            case 'Delete':     e.preventDefault(); this._deleteKey(); break;
            case 'Insert':     e.preventDefault(); this.insertMode = !this.insertMode; this._updateStatusBar(); break;
            case 'Tab':
                e.preventDefault();
                if (e.shiftKey) this._moveCursor(Math.max(0, this.cursorX - 8), this.cursorY);
                else this._moveCursor(Math.min(this.columns - 1, this.cursorX + 8), this.cursorY);
                break;
            default:
                if (!ctrl && !e.altKey && e.key.length === 1) {
                    e.preventDefault();
                    // Auto-grow if typing at the end of the last row
                    if (this.cursorY >= this.rows - 1 && this.cursorX >= this.columns - 1) {
                        this.doc.growRows(1); this.rows = this.doc.rows;
                        this._rebuildCanvas();
                    }
                    var code = charToCp437(e.key);
                    this._typeChar(code);
                }
                break;
        }
    }

    // ── Mouse ──

    _handleMouseDown(e) {
        e.preventDefault();
        this.el.root.focus();
        var pos = this._getCanvasXY(e);
        if (pos.x < 0 || pos.x >= this.columns || pos.y < 0 || pos.y >= this.rows) return;
        this._mouseDown = true;
        this._mouseButton = e.button;
        this._lastMouseX = pos.x;
        this._lastMouseY = pos.y;
        this._lastMouseHalfY = pos.halfY;

        if (SHAPE_TOOLS.includes(this.activeTool)) {
            this._shapeStartX = pos.x;
            this._shapeStartY = pos.y;
            this._createOverlay();
            return;
        }

        switch (this.activeTool) {
            case 'select':
                this._clearSelection();
                this._selDragStart = { x: pos.x, y: pos.y };
                this._selection = { sx: pos.x, sy: pos.y, dx: pos.x, dy: pos.y };
                this._updateSelectionOverlay();
                this._moveCursor(pos.x, pos.y);
                break;
            case 'brush': this.doc.startUndo(); this._brushDraw(pos.x, pos.y, pos.halfY, e.button); break;
            case 'fill': this._doFill(pos.x, pos.halfY, e.button); break;
            case 'sample': this._doSample(pos.x, pos.y); break;
        }
    }

    _handleMouseMove(e) {
        if (!this._mouseDown) return;
        var pos = this._getCanvasXY(e);

        if (SHAPE_TOOLS.includes(this.activeTool) && this._shapeOverlay) {
            // Clamp to canvas
            var mx = Math.max(0, Math.min(this.columns - 1, pos.x));
            var my = Math.max(0, Math.min(this.rows - 1, pos.y));
            this._updateShapeOverlay(this._shapeStartX, this._shapeStartY, mx, my);
            return;
        }

        if (pos.x < 0 || pos.x >= this.columns || pos.y < 0 || pos.y >= this.rows) return;
        if (pos.x === this._lastMouseX && pos.halfY === this._lastMouseHalfY) return;

        if (this.activeTool === 'brush') {
            this._brushDrawLine(this._lastMouseX, this._lastMouseHalfY, pos.x, pos.halfY, this._mouseButton);
        } else if (this.activeTool === 'select' && this._selDragStart) {
            var sx = Math.min(this._selDragStart.x, pos.x);
            var sy = Math.min(this._selDragStart.y, pos.y);
            var dx = Math.max(this._selDragStart.x, pos.x);
            var dy = Math.max(this._selDragStart.y, pos.y);
            this._selection = { sx: sx, sy: sy, dx: dx, dy: dy };
            this._updateSelectionOverlay();
            this._updateStatusBar();
            this._moveCursor(pos.x, pos.y);
        }
        this._lastMouseX = pos.x;
        this._lastMouseY = pos.y;
        this._lastMouseHalfY = pos.halfY;
    }

    _handleMouseUp(e) {
        if (!this._mouseDown) return;
        this._mouseDown = false;

        if (SHAPE_TOOLS.includes(this.activeTool) && this._shapeOverlay) {
            var pos = this._getCanvasXY(e);
            var mx = Math.max(0, Math.min(this.columns - 1, pos.x));
            var my = Math.max(0, Math.min(this.rows - 1, pos.y));
            this._commitShape(this._shapeStartX, this._shapeStartY, mx, my);
            this._destroyOverlay();
            return;
        }

        if (this.activeTool === 'brush') this.doc.endUndo();
        if (this.activeTool === 'select') {
            // If it was just a click (no drag), clear the selection
            if (this._selection && this._selection.sx === this._selection.dx
                && this._selection.sy === this._selection.dy) {
                this._clearSelection();
            }
            this._selDragStart = null;
            this._updateStatusBar();
        }
    }

    // ═══ SHAPE OVERLAY ═══

    _createOverlay() {
        const c = document.createElement('canvas');
        c.className = 'me-shape-overlay';
        c.width = this.renderer.width;
        c.height = this.renderer.height;
        c.style.width = this.el.canvas.style.width;
        c.style.height = this.el.canvas.style.height;
        this.el.editingLayer.appendChild(c);
        this._shapeOverlay = c;
    }

    _updateShapeOverlay(x0, y0, x1, y1) {
        const c = this._shapeOverlay;
        if (!c) return;
        const ctx = c.getContext('2d');
        ctx.clearRect(0, 0, c.width, c.height);

        const points = this._getShapePoints(x0, y0, x1, y1);
        const fw = this.font.width, fh = this.font.height;

        ctx.fillStyle = 'rgba(255, 255, 255, 0.4)';
        for (const p of points) {
            if (p.x >= 0 && p.x < this.columns && p.y >= 0 && p.y < this.rows) {
                ctx.fillRect(p.x * fw, p.y * fh, fw, fh);
            }
        }
    }

    _destroyOverlay() {
        if (this._shapeOverlay) {
            this._shapeOverlay.remove();
            this._shapeOverlay = null;
        }
    }

    _getShapePoints(x0, y0, x1, y1) {
        switch (this.activeTool) {
            case 'line':
                return bresenhamLine(x0, y0, x1, y1);
            case 'rect':
                return this.shapeFilled ? rectFilled(x0, y0, x1, y1) : rectOutline(x0, y0, x1, y1);
            case 'ellipse': {
                const { cx, cy, rx, ry } = ellipseFromDrag(x0, y0, x1, y1);
                return this.shapeFilled ? ellipseFilled(cx, cy, rx, ry) : ellipseOutline(cx, cy, rx, ry);
            }
            default:
                return [];
        }
    }

    _commitShape(x0, y0, x1, y1) {
        const points = this._getShapePoints(x0, y0, x1, y1);
        if (points.length === 0) return;

        // Use the current brush character (default: full block 219)
        const code = this.customBlockChar;
        this.doc.startUndo();
        const affected = [];
        for (const p of points) {
            if (p.x >= 0 && p.x < this.columns && p.y >= 0 && p.y < this.rows) {
                this.doc.changeData(p.x, p.y, code, this.fg, this.bg);
                affected.push({ x: p.x, y: p.y });
            }
        }
        this.doc.endUndo();
        this._renderCells(affected);
        this._updatePreview();
    }

    // ═══ FILE LOADING ═══

    _promptLoadFile() {
        const input = document.createElement('input');
        input.type = 'file';
        input.accept = '.ans,.bin,.asc,.diz,.nfo,.ice';
        input.onchange = () => {
            const file = input.files[0];
            if (!file) return;
            const reader = new FileReader();
            reader.onload = () => {
                const bytes = new Uint8Array(reader.result);
                this.loadBytes(bytes, file.name);
            };
            reader.readAsArrayBuffer(file);
        };
        input.click();
    }

    loadBytes(bytes, filename) {
        const info = detectFileType(filename, bytes);
        const sauce = info.sauce;
        const content = getContentBytes(bytes, sauce);
        const cols = info.columns;

        let result;
        if (info.type === 'bin') {
            result = loadBin(content, cols);
        } else {
            result = loadAnsi(content, cols, 0);
        }

        // Ensure grid is fully padded to cols*rows
        const totalCells = cols * result.rows;
        while (result.grid.length < totalCells) {
            result.grid.push({ code: 32, fg: 7, bg: 0 });
        }
        this.doc.replaceAll(result.grid, cols, result.rows);
        this.columns = cols;
        this.rows = result.rows;
        this._rebuildCanvas();
        this._moveCursor(0, 0);
        this._updateStatusBar();
    }

    // ═══ CANVAS RESIZE ═══

    _showResizeOverlay() {
        this.el.resizeColsInput.value = this.columns;
        this.el.resizeRowsInput.value = this.rows;
        this.el.backdrop.classList.add('me-visible');
        this.el.resizeOverlay.classList.add('me-visible');
        this.el.resizeColsInput.focus();
    }

    _doResize(newCols, newRows) {
        if (isNaN(newCols) || isNaN(newRows) || newCols < 1 || newRows < 1) return;
        this.doc.resize(newCols, newRows);
        this.columns = newCols;
        this.rows = newRows;
        this._rebuildCanvas();
        this._moveCursor(
            Math.min(this.cursorX, this.columns - 1),
            Math.min(this.cursorY, this.rows - 1)
        );
        this._hideOverlay();
        this._updateStatusBar();
    }

    setCanvasSize(cols, rows) {
        this._doResize(cols, rows);
    }

    _rebuildCanvas() {
        this.renderer = new Renderer(this.doc, this.font);
        this.renderer.render();
        const canvas = this.renderer.canvas;
        canvas.className = 'me-canvas';
        canvas.style.width = this.renderer.width + 'px';
        canvas.style.height = this.renderer.height + 'px';
        this.el.canvas.replaceWith(canvas);
        this.el.canvas = canvas;
        this._updatePreview();
    }

    // ═══ RENDERING ═══

    _fullRender() { this.renderer.render(); }
    _renderCell(x, y) { this.renderer.renderAt(x, y); }
    _renderCells(cells) { this.renderer.renderCells(cells); }

    // ═══ CURSOR ═══

    _updateCursor() {
        var cc = this.el.cursorCanvas;
        if (!cc) return;
        var ctx = cc.getContext('2d');
        ctx.clearRect(0, 0, cc.width, cc.height);
        var block = this.doc.at(this.cursorX, this.cursorY);
        if (block) this.font.draw(ctx, block, 0, 0);
        ctx.globalCompositeOperation = 'difference';
        ctx.fillStyle = '#ffffff';
        ctx.fillRect(0, this.font.height - 2, this.font.width, 2);
        ctx.globalCompositeOperation = 'source-over';
        cc.style.left = (this.cursorX * this.font.width) + 'px';
        cc.style.top = (this.cursorY * this.font.height) + 'px';
        this._updateStatusBar();
    }

    _moveCursor(x, y, scroll) {
        x = Math.max(0, Math.min(this.columns - 1, x));
        y = Math.max(0, Math.min(this.rows - 1, y));
        this.cursorX = x;
        this.cursorY = y;
        this._updateCursor();
        if (scroll !== false) this._scrollToCursor();
    }

    _scrollToCursor() {
        var vp = this.el.viewport;
        var px = this.cursorX * this.font.width;
        var py = this.cursorY * this.font.height;
        if (px < vp.scrollLeft) vp.scrollLeft = px;
        if (px + this.font.width > vp.scrollLeft + vp.clientWidth) vp.scrollLeft = px + this.font.width - vp.clientWidth;
        if (py < vp.scrollTop) vp.scrollTop = py;
        if (py + this.font.height > vp.scrollTop + vp.clientHeight) vp.scrollTop = py + this.font.height - vp.clientHeight;
    }

    // ═══ PALETTE ═══

    _setFg(color) { this.fg = color; this._updatePalette(); }
    _setBg(color) { this.bg = color; this._updatePalette(); }

    _updatePalette() {
        this.el.fgSwatch.style.backgroundColor = rgbString(ega[this.fg]);
        this.el.bgSwatch.style.backgroundColor = rgbString(ega[this.bg]);
        for (var i = 0; i < this.el.palCells.length; i++) {
            var cell = this.el.palCells[i];
            var ci = parseInt(cell.dataset.colorIndex || i);
            cell.classList.toggle('me-pal-fg', ci === this.fg);
            cell.classList.toggle('me-pal-bg', ci === this.bg);
        }
        this._updateFkeyDisplay();
    }

    // ═══ TOOLS ═══

    _selectTool(name) {
        this.activeTool = name;
        for (var t of TOOLS) {
            this.el.toolBtns[t].classList.toggle('me-active', t === name);
        }
        this.el.brushModes.classList.toggle('me-visible', name === 'brush');
        this.el.cursorCanvas.classList.toggle('me-hidden', name !== 'select');
        this.el.viewport.style.cursor = name === 'select' ? 'text' : 'crosshair';
        // Show fill toggle for rect/ellipse
        this.el.fillToggle.classList.toggle('me-visible', name === 'rect' || name === 'ellipse');
        this._updateToolbar();
        this._updateStatusBar();
    }

    _updateFillToggle() {
        this.el.fillOptOutline.classList.toggle('me-active', !this.shapeFilled);
        this.el.fillOptFilled.classList.toggle('me-active', this.shapeFilled);
    }

    // ── Select tool helpers ──

    _typeChar(code) {
        this.doc.startUndo();
        if (this.insertMode) {
            for (var x = this.columns - 1; x > this.cursorX; x--) {
                var prev = this.doc.at(x - 1, this.cursorY);
                if (prev) this.doc.changeData(x, this.cursorY, prev.code, prev.fg, prev.bg);
            }
        }
        this.doc.changeData(this.cursorX, this.cursorY, code, this.fg, this.bg);
        this.doc.endUndo();
        for (var rx = this.insertMode ? this.cursorX : this.cursorX; rx < this.columns; rx++) {
            this._renderCell(rx, this.cursorY);
            if (!this.insertMode) break;
        }
        this._moveCursor(this.cursorX + 1, this.cursorY);
        this._updatePreview();
    }

    _typeFkey(num) {
        var set = FKEY_SETS[this.fkeySetIndex];
        if (set && num < set.length) this._typeChar(set[num]);
    }

    _backspace() {
        if (this.cursorX > 0) {
            this._moveCursor(this.cursorX - 1, this.cursorY);
            this.doc.startUndo();
            this.doc.changeData(this.cursorX, this.cursorY, 32, this.fg, this.bg);
            this.doc.endUndo();
            this._renderCell(this.cursorX, this.cursorY);
            this._updateCursor();
        }
    }

    _deleteKey() {
        this.doc.startUndo();
        for (var x = this.cursorX; x < this.columns - 1; x++) {
            var next = this.doc.at(x + 1, this.cursorY);
            if (next) this.doc.changeData(x, this.cursorY, next.code, next.fg, next.bg);
        }
        this.doc.changeData(this.columns - 1, this.cursorY, 32, 7, 0);
        this.doc.endUndo();
        for (var rx = this.cursorX; rx < this.columns; rx++) this._renderCell(rx, this.cursorY);
        this._updateCursor();
    }

    // ── Brush tool ──

    _brushDraw(x, y, halfY, button) {
        var color = (button === 0) ? this.fg : this.bg;
        switch (this.brushMode) {
            case 'half_block': {
                var affected = this.doc.setHalfBlock(x, halfY, color);
                if (affected) this._renderCell(affected.x, affected.y);
                break;
            }
            case 'custom_block': {
                this.doc.changeData(x, y, this.customBlockChar, this.fg, this.bg);
                this._renderCell(x, y);
                break;
            }
            case 'shading': {
                var block = this.doc.at(x, y);
                var shades = [32, 176, 177, 178, 219];
                var idx = shades.indexOf(block ? block.code : 32);
                if (idx < 0) idx = 0;
                if (button === 0) idx = Math.min(idx + 1, shades.length - 1);
                else idx = Math.max(idx - 1, 0);
                this.doc.changeData(x, y, shades[idx], this.fg, this.bg);
                this._renderCell(x, y);
                break;
            }
        }
    }

    _brushDrawLine(x0, halfY0, x1, halfY1, button) {
        var dx = Math.abs(x1 - x0);
        var dy = Math.abs(halfY1 - halfY0);
        var sx = x0 < x1 ? 1 : -1;
        var sy = halfY0 < halfY1 ? 1 : -1;
        var err = dx - dy;
        while (true) {
            var y = Math.floor(halfY0 / 2);
            this._brushDraw(x0, y, halfY0, button);
            if (x0 === x1 && halfY0 === halfY1) break;
            var e2 = 2 * err;
            if (e2 > -dy) { err -= dy; x0 += sx; }
            if (e2 < dx) { err += dx; halfY0 += sy; }
        }
    }

    _setBrushMode(mode) { this.brushMode = mode; this._updateToolbar(); }

    // ── Fill tool ──

    _doFill(x, halfY, button) {
        var cellY = Math.floor(halfY / 2);
        var isUpper = (halfY % 2 === 0);
        var info = this.doc.getHalfBlock(x, cellY);
        if (!info) return;
        var targetColor = isUpper ? info.upperColor : info.lowerColor;
        var fillColor = (button === 0) ? this.fg : this.bg;
        if (targetColor === fillColor) return;

        this.doc.startUndo();
        var visited = {};
        var stack = [{ x: x, halfY: halfY }];
        var affected = {};

        while (stack.length > 0) {
            var pt = stack.pop();
            var key = pt.x + ',' + pt.halfY;
            if (visited[key]) continue;
            visited[key] = true;
            var cy = Math.floor(pt.halfY / 2);
            var isUp = (pt.halfY % 2 === 0);
            if (pt.x < 0 || pt.x >= this.columns || cy < 0 || cy >= this.rows) continue;
            var hb = this.doc.getHalfBlock(pt.x, cy);
            if (!hb) continue;
            var currentColor = isUp ? hb.upperColor : hb.lowerColor;
            if (currentColor !== targetColor) continue;
            this.doc.setHalfBlock(pt.x, pt.halfY, fillColor);
            affected[pt.x + ',' + cy] = { x: pt.x, y: cy };
            stack.push({ x: pt.x - 1, halfY: pt.halfY });
            stack.push({ x: pt.x + 1, halfY: pt.halfY });
            stack.push({ x: pt.x, halfY: pt.halfY - 1 });
            stack.push({ x: pt.x, halfY: pt.halfY + 1 });
        }
        this.doc.endUndo();
        for (var k in affected) this._renderCell(affected[k].x, affected[k].y);
        this._updatePreview();
    }

    // ── Sample tool ──

    _doSample(x, y) {
        var block = this.doc.at(x, y);
        if (!block) return;
        this._setFg(block.fg);
        this._setBg(block.bg);
        this._selectTool('select');
    }

    // ═══ OVERLAYS ═══

    _showAttributeOverlay(target) {
        this._attrTarget = target || 'fg';
        this.el.attrTitle.textContent = this._attrTarget === 'fg' ? 'Foreground Color' : 'Background Color';
        var selected = this._attrTarget === 'fg' ? this.fg : this.bg;
        for (var i = 0; i < 16; i++) {
            this.el.attrCells[i].classList.toggle('me-selected', i === selected);
        }
        this.el.backdrop.classList.add('me-visible');
        this.el.attrOverlay.classList.add('me-visible');
    }

    _hideOverlay() {
        this.el.backdrop.classList.remove('me-visible');
        this.el.attrOverlay.classList.remove('me-visible');
        this.el.resizeOverlay.classList.remove('me-visible');
    }

    // Keep legacy name for compatibility
    _hideAttributeOverlay() { this._hideOverlay(); }

    _onAttrPick(color) {
        if (this._attrTarget === 'fg') this._setFg(color);
        else this._setBg(color);
        this._hideOverlay();
    }

    // ═══ SELECTION ═══

    _updateSelectionOverlay() {
        var ov = this.el.selectionOverlay;
        if (!ov) return;
        if (!this._selection) { ov.classList.add('me-hidden'); return; }
        var s = this._selection;
        var fw = this.font.width, fh = this.font.height;
        ov.style.left = (s.sx * fw) + 'px';
        ov.style.top = (s.sy * fh) + 'px';
        ov.style.width = ((s.dx - s.sx + 1) * fw) + 'px';
        ov.style.height = ((s.dy - s.sy + 1) * fh) + 'px';
        ov.classList.remove('me-hidden');
    }

    _clearSelection() {
        this._selection = null;
        this._selDragStart = null;
        if (this.el.selectionOverlay) this.el.selectionOverlay.classList.add('me-hidden');
    }

    _getSelectionBlocks() {
        if (!this._selection) return null;
        var s = this._selection;
        var cols = s.dx - s.sx + 1;
        var rows = s.dy - s.sy + 1;
        var data = [];
        for (var y = s.sy; y <= s.dy; y++) {
            for (var x = s.sx; x <= s.dx; x++) {
                var block = this.doc.at(x, y);
                data.push(block ? { code: block.code, fg: block.fg, bg: block.bg } : { code: 32, fg: 7, bg: 0 });
            }
        }
        return { columns: cols, rows: rows, data: data };
    }

    _copySelection() {
        this._clipboard = this._getSelectionBlocks();
    }

    _cutSelection() {
        this._clipboard = this._getSelectionBlocks();
        this._eraseSelection();
    }

    _eraseSelection() {
        if (!this._selection) return;
        var s = this._selection;
        this.doc.startUndo();
        var affected = [];
        for (var y = s.sy; y <= s.dy; y++) {
            for (var x = s.sx; x <= s.dx; x++) {
                this.doc.changeData(x, y, 32, 7, 0);
                affected.push({ x, y });
            }
        }
        this.doc.endUndo();
        this._renderCells(affected);
        this._clearSelection();
        this._updatePreview();
    }

    _pasteClipboard() {
        if (!this._clipboard) return;
        var cb = this._clipboard;
        this.doc.startUndo();
        var affected = [];
        for (var cy = 0; cy < cb.rows; cy++) {
            for (var cx = 0; cx < cb.columns; cx++) {
                var tx = this.cursorX + cx;
                var ty = this.cursorY + cy;
                if (tx < this.columns && ty < this.rows) {
                    var block = cb.data[cy * cb.columns + cx];
                    this.doc.changeData(tx, ty, block.code, block.fg, block.bg);
                    affected.push({ x: tx, y: ty });
                }
            }
        }
        this.doc.endUndo();
        this._renderCells(affected);
        this._clearSelection();
        this._updatePreview();
    }

    // ═══ UNDO/REDO ═══

    _undo() {
        var affected = this.doc.undo();
        if (affected.length > 0) { this._renderCells(affected); this._updateCursor(); this._updatePreview(); }
    }
    _redo() {
        var affected = this.doc.redo();
        if (affected.length > 0) { this._renderCells(affected); this._updateCursor(); this._updatePreview(); }
    }

    // ═══ TOOLBAR ═══

    _updateToolbar() {
        for (var mode of BRUSH_MODES) {
            this.el.brushModeBtns[mode].classList.toggle('me-active', mode === this.brushMode);
        }
        this._updateFkeyDisplay();
    }

    _updateFkeyDisplay() {
        var set = FKEY_SETS[this.fkeySetIndex];
        for (var i = 0; i < 12; i++) {
            var canvas = this.el.fkeyCanvases[i];
            var ctx = canvas.getContext('2d');
            canvas.width = this.font.width;
            canvas.height = this.font.height;
            this.font.draw(ctx, { code: set[i], fg: this.fg, bg: this.bg }, 0, 0);
        }
    }

    _nextFkeySet() {
        this.fkeySetIndex = (this.fkeySetIndex + 1) % FKEY_SETS.length;
        this._updateFkeyDisplay();
    }
    _prevFkeySet() {
        this.fkeySetIndex = (this.fkeySetIndex - 1 + FKEY_SETS.length) % FKEY_SETS.length;
        this._updateFkeyDisplay();
    }

    // ═══ STATUS BAR ═══

    _updateStatusBar() {
        this.el.statusPos.textContent = 'Ln ' + (this.cursorY + 1) + ', Col ' + (this.cursorX + 1);
        this.el.statusDim.textContent = this.columns + ' \u00D7 ' + this.rows;
        this.el.statusMode.textContent = this.insertMode ? 'INS' : 'OVR';
        if (this._selection) {
            var s = this._selection;
            var sw = s.dx - s.sx + 1, sh = s.dy - s.sy + 1;
            this.el.statusTool.textContent = 'Selection: ' + sw + '\u00D7' + sh;
        } else {
            this.el.statusTool.textContent = TOOL_TIPS[this.activeTool] || '';
        }
    }

    // ═══ PUBLIC API ═══

    getAnsiData() { return encodeAsAnsi(this.doc, { iceColors: this.iceColors }); }

    done() { this.onDone(this.getAnsiData()); }
    cancel() { this.onCancel(); }

    destroy() {
        if (this.el.root) this.el.root.removeEventListener('keydown', this._handlers.keydown);
        document.removeEventListener('mousemove', this._handlers.mousemove);
        document.removeEventListener('mouseup', this._handlers.mouseup);
        if (this.container) this.container.innerHTML = '';
    }
}

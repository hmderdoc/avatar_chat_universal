/**
 * ANSI Editor Modal — Draggable, Resizable Window
 *
 * Wraps the AnsiEditor in a floating modal that behaves like a
 * desktop window: title bar for dragging, resize handle in the
 * bottom-right corner.  Works for both chat and forum contexts.
 *
 * Usage:
 *   AnsiEditorModal.open({
 *       title:    'ANSI Editor',
 *       fontUrl:  './fonts/ansi-editor/IBM VGA.F16',
 *       columns:  80,
 *       rows:     25,
 *       onDone:   function (editor) { ... },  // editor.doc still valid
 *       onCancel: function () { ... }
 *   });
 *
 *   AnsiEditorModal.close();   // programmatic close
 */

(function () {
    'use strict';

    /* ── State ── */
    var overlay = null;
    var modal = null;
    var editorInstance = null;
    var _onDone = null;
    var _onCancel = null;
    var _onReady = null;

    /* ── Geometry defaults ── */
    var DEFAULT_WIDTH  = 980;
    var DEFAULT_HEIGHT = 620;
    var MIN_WIDTH  = 480;
    var MIN_HEIGHT = 360;

    /* ── Public API ── */
    var AnsiEditorModal = {
        open: open,
        close: close,
        get editor() { return editorInstance; },
        get isOpen() { return !!modal; }
    };

    function open(opts) {
        if (modal) close();
        if (!window.AnsiEditor) {
            console.error('AnsiEditor not loaded');
            return;
        }

        opts = opts || {};
        _onDone = opts.onDone || null;
        _onCancel = opts.onCancel || null;
        _onReady = opts.onReady || null;

        /* ── Backdrop ── */
        overlay = document.createElement('div');
        overlay.className = 'ae-modal-overlay';
        overlay.addEventListener('mousedown', function (e) {
            if (e.target === overlay) {
                if (modal) modal.classList.add('ae-modal-shake');
                setTimeout(function () {
                    if (modal) modal.classList.remove('ae-modal-shake');
                }, 300);
            }
        });

        /* ── Modal window ── */
        var minWidth = typeof opts.minWidth === 'number' ? opts.minWidth : MIN_WIDTH;
        var minHeight = typeof opts.minHeight === 'number' ? opts.minHeight : MIN_HEIGHT;
        var width = typeof opts.width === 'number' ? Math.max(minWidth, opts.width) : DEFAULT_WIDTH;
        var height = typeof opts.height === 'number' ? Math.max(minHeight, opts.height) : DEFAULT_HEIGHT;

        modal = document.createElement('div');
        modal.className = 'ae-modal-window';
        if (opts.className) modal.className += ' ' + opts.className;
        modal.style.width = width + 'px';
        modal.style.height = height + 'px';
        modal.style.minWidth = minWidth + 'px';
        modal.style.minHeight = minHeight + 'px';

        var left = Math.max(0, (window.innerWidth - width) / 2);
        var top  = Math.max(0, (window.innerHeight - height) / 2);
        modal.style.left = left + 'px';
        modal.style.top  = top  + 'px';

        /* ── Title bar ── */
        var titleBar = document.createElement('div');
        titleBar.className = 'ae-modal-titlebar';

        var titleText = document.createElement('span');
        titleText.className = 'ae-modal-title';
        titleText.textContent = opts.title || 'ANSI Editor';
        titleBar.appendChild(titleText);

        var closeBtn = document.createElement('button');
        closeBtn.className = 'ae-modal-close';
        closeBtn.textContent = '\u2715';
        closeBtn.title = 'Close';
        closeBtn.addEventListener('click', function () { doCancel(); });
        titleBar.appendChild(closeBtn);

        /* ── Editor container ── */
        var editorWrap = document.createElement('div');
        editorWrap.className = 'ae-modal-body';

        /* ── Resize handle ── */
        var resizeHandle = document.createElement('div');
        resizeHandle.className = 'ae-modal-resize';

        /* Assemble */
        modal.appendChild(titleBar);
        modal.appendChild(editorWrap);
        modal.appendChild(resizeHandle);
        overlay.appendChild(modal);
        document.body.appendChild(overlay);

        /* ── Dragging ── */
        initDrag(titleBar, modal);

        /* ── Resizing ── */
        initResize(resizeHandle, modal, minWidth, minHeight);

        /* ── Create the ANSI editor inside ── */
        AnsiEditor.create({
            container: editorWrap,
            columns:  opts.columns || 80,
            rows:     opts.rows    || 25,
            fontUrl:  opts.fontUrl || './fonts/ansi-editor/IBM VGA.F16',
            onDone:   function () { doDone(); },
            onCancel: function () { doCancel(); }
        }).then(function (editor) {
            editorInstance = editor;
            if (_onReady) {
                try {
                    _onReady(editor);
                } catch (err) {
                    console.error('ANSI Editor modal onReady error:', err);
                }
            }
        }).catch(function (err) {
            console.error('ANSI Editor modal error:', err);
            close();
        });

        document.body.style.overflow = 'hidden';
        document.addEventListener('keydown', onEsc);
    }

    function teardown() {
        document.removeEventListener('keydown', onEsc);
        if (overlay && overlay.parentNode) {
            overlay.parentNode.removeChild(overlay);
        }
        overlay = null;
        modal = null;
        document.body.style.overflow = '';
    }

    function close() {
        if (editorInstance) {
            try { editorInstance.destroy(); } catch (e) {}
            editorInstance = null;
        }
        _onDone = null;
        _onCancel = null;
        _onReady = null;
        teardown();
    }

    function doDone() {
        var cb = _onDone;
        var ed = editorInstance;
        // Capture ANSI data while editor is still fully alive in the DOM
        var ansiData = null;
        if (ed && ed.getAnsiData) {
            try { ansiData = ed.getAnsiData(); } catch (e) {}
        }
        // Detach so close() won't destroy the editor
        editorInstance = null;
        _onDone = null;
        _onCancel = null;
        _onReady = null;
        teardown();
        // Pass both editor (for doc.data access) and pre-captured ANSI bytes
        if (cb) cb(ed, ansiData);
        if (ed) { try { ed.destroy(); } catch (e) {} }
    }

    function doCancel() {
        var cb = _onCancel;
        close();
        if (cb) cb();
    }

    function onEsc(e) {
        if (e.key === 'Escape') doCancel();
    }

    /* ── Drag logic ── */
    function initDrag(handle, win) {
        var startX, startY, origLeft, origTop;

        function onMouseDown(e) {
            if (e.target.tagName === 'BUTTON') return;
            e.preventDefault();
            startX = e.clientX;
            startY = e.clientY;
            origLeft = win.offsetLeft;
            origTop  = win.offsetTop;
            document.addEventListener('mousemove', onMouseMove);
            document.addEventListener('mouseup', onMouseUp);
            handle.style.cursor = 'grabbing';
        }
        function onMouseMove(e) {
            var dx = e.clientX - startX;
            var dy = e.clientY - startY;
            win.style.left = (origLeft + dx) + 'px';
            win.style.top  = (origTop  + dy) + 'px';
        }
        function onMouseUp() {
            document.removeEventListener('mousemove', onMouseMove);
            document.removeEventListener('mouseup', onMouseUp);
            handle.style.cursor = '';
        }
        handle.addEventListener('mousedown', onMouseDown);
    }

    /* ── Resize logic ── */
    function initResize(handle, win, minWidth, minHeight) {
        var startX, startY, origW, origH;

        function onMouseDown(e) {
            e.preventDefault();
            e.stopPropagation();
            startX = e.clientX;
            startY = e.clientY;
            origW = win.offsetWidth;
            origH = win.offsetHeight;
            document.addEventListener('mousemove', onMouseMove);
            document.addEventListener('mouseup', onMouseUp);
        }
        function onMouseMove(e) {
            var w = Math.max(minWidth,  origW + (e.clientX - startX));
            var h = Math.max(minHeight, origH + (e.clientY - startY));
            win.style.width  = w + 'px';
            win.style.height = h + 'px';
        }
        function onMouseUp() {
            document.removeEventListener('mousemove', onMouseMove);
            document.removeEventListener('mouseup', onMouseUp);
        }
        handle.addEventListener('mousedown', onMouseDown);
    }

    /* ── Expose ── */
    window.AnsiEditorModal = AnsiEditorModal;

})();

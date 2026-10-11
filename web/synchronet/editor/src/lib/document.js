/**
 * ANSI art document model with undo/redo.
 *
 * The document is a flat grid of {code, fg, bg} blocks.
 * Includes half-block helpers for brush drawing at double-height resolution.
 */

export class TextDocument {
    constructor(columns, rows) {
        this.columns = columns;
        this.rows = rows;
        this.data = [];
        this.undoStack = [];
        this.redoStack = [];
        this._currentUndo = null;

        // Initialize with spaces, light gray on black
        for (let i = 0; i < columns * rows; i++) {
            this.data.push({ code: 32, fg: 7, bg: 0 });
        }
    }

    at(x, y) {
        if (x < 0 || x >= this.columns || y < 0 || y >= this.rows) return null;
        return this.data[y * this.columns + x];
    }

    // ── Editing with undo tracking ──

    startUndo() {
        this._currentUndo = [];
    }

    changeData(x, y, code, fg, bg) {
        if (x < 0 || x >= this.columns || y < 0 || y >= this.rows) return null;
        const idx = y * this.columns + x;
        const old = { ...this.data[idx] };

        if (this._currentUndo) {
            this._currentUndo.push({ x, y, old });
        }

        this.data[idx] = { code, fg, bg };
        return { x, y };
    }

    endUndo() {
        if (this._currentUndo && this._currentUndo.length > 0) {
            this.undoStack.push(this._currentUndo);
            this.redoStack = [];
        }
        this._currentUndo = null;
    }

    undo() {
        if (this.undoStack.length === 0) return [];
        const changes = this.undoStack.pop();
        const redo = [];
        const affected = [];

        // Replay newest-first so cells touched multiple times in one
        // group end at their original value, not an intermediate one
        for (let i = changes.length - 1; i >= 0; i--) {
            const ch = changes[i];
            const idx = ch.y * this.columns + ch.x;
            redo.push({ x: ch.x, y: ch.y, old: { ...this.data[idx] } });
            this.data[idx] = { ...ch.old };
            affected.push({ x: ch.x, y: ch.y });
        }

        this.redoStack.push(redo);
        return affected;
    }

    redo() {
        if (this.redoStack.length === 0) return [];
        const changes = this.redoStack.pop();
        const undo = [];
        const affected = [];

        for (let i = changes.length - 1; i >= 0; i--) {
            const ch = changes[i];
            const idx = ch.y * this.columns + ch.x;
            undo.push({ x: ch.x, y: ch.y, old: { ...this.data[idx] } });
            this.data[idx] = { ...ch.old };
            affected.push({ x: ch.x, y: ch.y });
        }

        this.undoStack.push(undo);
        return affected;
    }

    // ── Half-block helpers ──
    // Half-block mode divides each character cell vertically into two halves.
    // Uses block characters: 32=space, 219=full, 220=lower, 223=upper.

    getHalfBlock(x, y) {
        const block = this.at(x, y);
        if (!block) return null;

        const { code, fg, bg } = block;
        let upperColor, lowerColor;

        switch (code) {
            case 32:   upperColor = bg; lowerColor = bg; break;  // space: all bg
            case 219:  upperColor = fg; lowerColor = fg; break;  // full block: all fg
            case 220:  upperColor = bg; lowerColor = fg; break;  // lower half
            case 223:  upperColor = fg; lowerColor = bg; break;  // upper half
            default:
                // Non-block char: treat upper as fg, lower as bg
                return { isBlocky: false, upperColor: fg, lowerColor: bg, code, fg, bg };
        }

        return { isBlocky: true, upperColor, lowerColor, code, fg, bg };
    }

    /**
     * Set a half-block pixel.
     * @param {number} x       Column
     * @param {number} halfY   Row in half-block coords (0 = top of row 0, 1 = bottom of row 0, etc.)
     * @param {number} color   Palette color index (0-15)
     * @returns {{ x, y }|null} Affected cell coordinates
     */
    setHalfBlock(x, halfY, color) {
        const cellY = Math.floor(halfY / 2);
        const isUpper = (halfY % 2 === 0);

        const info = this.getHalfBlock(x, cellY);
        if (!info) return null;

        let upper = isUpper ? color : info.upperColor;
        let lower = isUpper ? info.lowerColor : color;

        let newCode, newFg, newBg;

        if (upper === lower) {
            if (upper === 0) {
                newCode = 32; newFg = 0; newBg = 0;
            } else {
                newCode = 219; newFg = upper; newBg = 0;
            }
        } else {
            // Use upper-half block (223): fg = upper, bg = lower
            newCode = 223; newFg = upper; newBg = lower;
        }

        return this.changeData(x, cellY, newCode, newFg, newBg);
    }

    // ── Row operations ──

    insertRow(y) {
        if (y < 0 || y > this.rows) return;
        const newRow = [];
        for (let i = 0; i < this.columns; i++) {
            newRow.push({ code: 32, fg: 7, bg: 0 });
        }
        this.data.splice(y * this.columns, 0, ...newRow);
        this.rows++;
        // Trim excess if needed (keep last row from growing unbounded)
    }

    deleteRow(y) {
        if (y < 0 || y >= this.rows || this.rows <= 1) return;
        this.data.splice(y * this.columns, this.columns);
        this.rows--;
    }

    eraseLine(y) {
        if (y < 0 || y >= this.rows) return;
        const start = y * this.columns;
        for (let i = 0; i < this.columns; i++) {
            this.data[start + i] = { code: 32, fg: 7, bg: 0 };
        }
    }

    /**
     * Resize the document canvas. Preserves existing content that fits.
     * @param {number} newColumns
     * @param {number} newRows
     */
    resize(newColumns, newRows) {
        if (newColumns < 1 || newRows < 1) return;
        if (newColumns > 3000 || newRows > 10000) return;
        if (newColumns === this.columns && newRows === this.rows) return;

        const newData = new Array(newColumns * newRows);
        for (let i = 0; i < newData.length; i++) {
            newData[i] = { code: 32, fg: 7, bg: 0 };
        }

        const copyRows = Math.min(this.rows, newRows);
        const copyCols = Math.min(this.columns, newColumns);
        for (let y = 0; y < copyRows; y++) {
            for (let x = 0; x < copyCols; x++) {
                newData[y * newColumns + x] = this.data[y * this.columns + x];
            }
        }

        this.data = newData;
        this.columns = newColumns;
        this.rows = newRows;
        // Clear undo/redo since indices are invalidated
        this.undoStack = [];
        this.redoStack = [];
        this._currentUndo = null;
    }

    /**
     * Add rows at the bottom of the document.
     * @param {number} count  Number of rows to add
     */
    growRows(count) {
        for (let r = 0; r < count; r++) {
            for (let c = 0; c < this.columns; c++) {
                this.data.push({ code: 32, fg: 7, bg: 0 });
            }
            this.rows++;
        }
    }

    /**
     * Replace entire document data (used by file loader).
     * @param {Array} data     Flat array of {code, fg, bg}
     * @param {number} columns
     * @param {number} rows
     */
    replaceAll(data, columns, rows) {
        this.data = data;
        this.columns = columns;
        this.rows = rows;
        this.undoStack = [];
        this.redoStack = [];
        this._currentUndo = null;
    }
}

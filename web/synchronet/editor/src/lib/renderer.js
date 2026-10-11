/**
 * Canvas rendering engine — draws a TextDocument using a Font.
 */

export class Renderer {
    constructor(doc, font) {
        this.doc = doc;
        this.font = font;
        this.canvas = null;
        this.ctx = null;
        this.width = 0;
        this.height = 0;
    }

    /**
     * Full render of the entire document. Creates the canvas if needed.
     */
    render() {
        this.width = this.font.width * this.doc.columns;
        this.height = this.font.height * this.doc.rows;

        if (!this.canvas) {
            this.canvas = document.createElement('canvas');
        }
        this.canvas.width = this.width;
        this.canvas.height = this.height;
        this.ctx = this.canvas.getContext('2d');

        for (let y = 0, i = 0; y < this.doc.rows; y++) {
            for (let x = 0; x < this.doc.columns; x++, i++) {
                this.font.draw(
                    this.ctx,
                    this.doc.data[i],
                    x * this.font.width,
                    y * this.font.height
                );
            }
        }
    }

    /**
     * Re-render a single cell.
     */
    renderAt(x, y) {
        const block = this.doc.at(x, y);
        if (block && this.ctx) {
            this.font.draw(this.ctx, block, x * this.font.width, y * this.font.height);
        }
    }

    /**
     * Re-render a batch of cells.
     * @param {Array<{x:number, y:number}>} cells
     */
    renderCells(cells) {
        if (!this.ctx) return;
        for (const { x, y } of cells) {
            const block = this.doc.at(x, y);
            if (block) {
                this.font.draw(this.ctx, block, x * this.font.width, y * this.font.height);
            }
        }
    }
}

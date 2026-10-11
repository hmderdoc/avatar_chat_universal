/**
 * Bitmap font loader and character renderer for CP437 fonts.
 * Font files are simple binary: 256 characters × height bytes per char.
 * Each byte is 8 pixels wide, MSB = leftmost pixel.
 */

export class Font {
    constructor() {
        this.width = 8;
        this.height = 16;
        this.bitmask = null;   // Uint8Array
        this.palette = null;   // Array of {r,g,b}
    }

    /**
     * Load font data.
     * @param {Object} opts
     * @param {string} [opts.url]     URL to fetch font binary from
     * @param {Uint8Array} [opts.data] Raw font bytes
     * @param {Array} opts.palette     Array of 16 {r,g,b} objects
     */
    async load(opts) {
        this.palette = opts.palette;

        if (opts.data) {
            this.bitmask = opts.data instanceof Uint8Array ? opts.data : new Uint8Array(opts.data);
        } else if (opts.url) {
            const resp = await fetch(opts.url);
            if (!resp.ok) throw new Error(`Font fetch failed: ${resp.status} ${opts.url}`);
            this.bitmask = new Uint8Array(await resp.arrayBuffer());
        } else {
            throw new Error('Font.load requires url or data');
        }

        this.height = this.bitmask.length / 256;
        if (this.height !== Math.floor(this.height) || this.height < 1) {
            throw new Error(`Invalid font data: ${this.bitmask.length} bytes`);
        }
    }

    /**
     * Draw a single character cell to a canvas context.
     * @param {CanvasRenderingContext2D} ctx
     * @param {{code:number, fg:number, bg:number}} block
     * @param {number} px  Pixel x position
     * @param {number} py  Pixel y position
     */
    draw(ctx, block, px, py) {
        const fgColor = this.palette[block.fg] || this.palette[7];
        const bgColor = this.palette[block.bg] || this.palette[0];
        const offset = (block.code & 0xFF) * this.height;

        const imageData = ctx.createImageData(this.width, this.height);
        const d = imageData.data;

        for (let row = 0; row < this.height; row++) {
            const byte = this.bitmask[offset + row] || 0;
            for (let col = 0; col < this.width; col++) {
                const bit = (byte >> (7 - col)) & 1;
                const c = bit ? fgColor : bgColor;
                const idx = (row * this.width + col) * 4;
                d[idx]     = c.r;
                d[idx + 1] = c.g;
                d[idx + 2] = c.b;
                d[idx + 3] = 255;
            }
        }

        ctx.putImageData(imageData, px, py);
    }

    /**
     * Draw cursor underline.
     */
    drawCursor(ctx, px, py) {
        ctx.fillStyle = '#ffffff';
        ctx.fillRect(px, py + this.height - 2, this.width, 2);
    }

    getRgb(index) {
        return this.palette[index] || this.palette[0];
    }
}

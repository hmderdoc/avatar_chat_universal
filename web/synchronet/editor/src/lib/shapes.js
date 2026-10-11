/**
 * Shape drawing algorithms for the ANSI editor.
 * All functions return arrays of {x, y} coordinate objects.
 */

/**
 * Bresenham's line algorithm.
 * @returns {Array<{x:number, y:number}>}
 */
export function bresenhamLine(x0, y0, x1, y1) {
    const points = [];
    let dx = Math.abs(x1 - x0), dy = Math.abs(y1 - y0);
    let sx = x0 < x1 ? 1 : -1, sy = y0 < y1 ? 1 : -1;
    let err = dx - dy;
    let cx = x0, cy = y0;

    while (true) {
        points.push({ x: cx, y: cy });
        if (cx === x1 && cy === y1) break;
        const e2 = 2 * err;
        if (e2 > -dy) { err -= dy; cx += sx; }
        if (e2 < dx) { err += dx; cy += sy; }
    }
    return points;
}

/**
 * Rectangle outline — 4 lines, deduplicated corners.
 * @returns {Array<{x:number, y:number}>}
 */
export function rectOutline(x0, y0, x1, y1) {
    const minX = Math.min(x0, x1), maxX = Math.max(x0, x1);
    const minY = Math.min(y0, y1), maxY = Math.max(y0, y1);
    const set = new Set();
    const pts = [];
    const add = (x, y) => {
        const k = x + ',' + y;
        if (!set.has(k)) { set.add(k); pts.push({ x, y }); }
    };
    for (let x = minX; x <= maxX; x++) { add(x, minY); add(x, maxY); }
    for (let y = minY + 1; y < maxY; y++) { add(minX, y); add(maxX, y); }
    return pts;
}

/**
 * Filled rectangle — scan rows.
 * @returns {Array<{x:number, y:number}>}
 */
export function rectFilled(x0, y0, x1, y1) {
    const minX = Math.min(x0, x1), maxX = Math.max(x0, x1);
    const minY = Math.min(y0, y1), maxY = Math.max(y0, y1);
    const pts = [];
    for (let y = minY; y <= maxY; y++) {
        for (let x = minX; x <= maxX; x++) {
            pts.push({ x, y });
        }
    }
    return pts;
}

/**
 * Midpoint ellipse algorithm — outline only.
 * @param {number} cx center x
 * @param {number} cy center y
 * @param {number} rx radius x
 * @param {number} ry radius y
 * @returns {Array<{x:number, y:number}>}
 */
export function ellipseOutline(cx, cy, rx, ry) {
    if (rx <= 0 || ry <= 0) return [{ x: cx, y: cy }];
    const set = new Set();
    const pts = [];
    const add = (x, y) => {
        const k = x + ',' + y;
        if (!set.has(k)) { set.add(k); pts.push({ x, y }); }
    };
    const plot4 = (x, y) => {
        add(cx + x, cy + y);
        add(cx - x, cy + y);
        add(cx + x, cy - y);
        add(cx - x, cy - y);
    };

    let x = 0, y = ry;
    let rx2 = rx * rx, ry2 = ry * ry;
    let px = 0, py = 2 * rx2 * y;
    let p;

    // Region 1
    p = ry2 - rx2 * ry + 0.25 * rx2;
    while (px < py) {
        plot4(x, y);
        x++;
        px += 2 * ry2;
        if (p < 0) {
            p += ry2 + px;
        } else {
            y--;
            py -= 2 * rx2;
            p += ry2 + px - py;
        }
    }

    // Region 2
    p = ry2 * (x + 0.5) * (x + 0.5) + rx2 * (y - 1) * (y - 1) - rx2 * ry2;
    while (y >= 0) {
        plot4(x, y);
        y--;
        py -= 2 * rx2;
        if (p > 0) {
            p += rx2 - py;
        } else {
            x++;
            px += 2 * ry2;
            p += rx2 - py + px;
        }
    }
    return pts;
}

/**
 * Filled ellipse — scan all points inside.
 * @returns {Array<{x:number, y:number}>}
 */
export function ellipseFilled(cx, cy, rx, ry) {
    if (rx <= 0 || ry <= 0) return [{ x: cx, y: cy }];
    const pts = [];
    for (let y = cy - ry; y <= cy + ry; y++) {
        for (let x = cx - rx; x <= cx + rx; x++) {
            const dx = x - cx, dy = y - cy;
            if ((dx * dx) / (rx * rx) + (dy * dy) / (ry * ry) <= 1.0) {
                pts.push({ x, y });
            }
        }
    }
    return pts;
}

/**
 * Convert drag start/end to ellipse center and radii.
 * The drag defines a bounding box; the ellipse is inscribed.
 */
export function ellipseFromDrag(sx, sy, dx, dy) {
    const cx = Math.round((sx + dx) / 2);
    const cy = Math.round((sy + dy) / 2);
    const rx = Math.abs(Math.round((dx - sx) / 2));
    const ry = Math.abs(Math.round((dy - sy) / 2));
    return { cx, cy, rx, ry };
}

/**
 * EGA 16-color palette.
 */
export const ega = [
    { r:   0, g:   0, b:   0 },   // 0  Black
    { r:   0, g:   0, b: 170 },   // 1  Blue
    { r:   0, g: 170, b:   0 },   // 2  Green
    { r:   0, g: 170, b: 170 },   // 3  Cyan
    { r: 170, g:   0, b:   0 },   // 4  Red
    { r: 170, g:   0, b: 170 },   // 5  Magenta
    { r: 170, g:  85, b:   0 },   // 6  Brown
    { r: 170, g: 170, b: 170 },   // 7  Light Gray
    { r:  85, g:  85, b:  85 },   // 8  Dark Gray
    { r:  85, g:  85, b: 255 },   // 9  Light Blue
    { r:  85, g: 255, b:  85 },   // 10 Light Green
    { r:  85, g: 255, b: 255 },   // 11 Light Cyan
    { r: 255, g:  85, b:  85 },   // 12 Light Red
    { r: 255, g:  85, b: 255 },   // 13 Light Magenta
    { r: 255, g: 255, b:  85 },   // 14 Yellow
    { r: 255, g: 255, b: 255 },   // 15 White
];

export function rgbString(c) {
    return `rgb(${c.r},${c.g},${c.b})`;
}

export function rgbHex(c) {
    return '#' + [c.r, c.g, c.b].map(v => v.toString(16).padStart(2, '0')).join('');
}

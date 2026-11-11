// Ours: what shares.js uses of qrcode-generator, for the check of the JavaScript (scripts/test.sh js).
interface QRCode {
  addData(data: string): void;
  make(): void;
  getModuleCount(): number;
  isDark(row: number, col: number): boolean;
}
declare const qrcode: (typeNumber: number, errorCorrectionLevel: 'L' | 'M' | 'Q' | 'H') => QRCode;
export default qrcode;

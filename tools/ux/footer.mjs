// Status line and header close-ups (2x) in both themes, plus the shape (keys
// and types only, never values) of the read-only GET endpoints Home uses.
//   node tools/ux/footer.mjs <outdir>        (same env as capture.mjs)
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { BASE, connect, sleep, startChrome, token } from "./cdp.mjs";

const OUT = process.argv[2];
if (!OUT) { console.error("usage: footer.mjs <outdir>"); process.exit(2); }
mkdirSync(OUT, { recursive: true });
const tok = token();
const chrome = await startChrome(join(OUT, "_profile-footer"));
const { send, ev, close } = await connect(chrome.port);
await send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 900, deviceScaleFactor: 2, mobile: false });
await send("Emulation.setTouchEmulationEnabled", { enabled: false });
await send("Emulation.setEmulatedMedia", { features: [] });
await send("Page.navigate", { url: `${BASE}/?t=${tok}` });
await sleep(3500);
for (const th of ["dark", "light"]) {
	await ev(`(()=>{const b=document.querySelector('[data-theme-set="${th}"]'); if(b) b.click(); return 1})()`);
	await sleep(600);
	const sl = JSON.parse(await ev(`JSON.stringify((document.querySelector('.statusline')||document.body).getBoundingClientRect())`));
	const y = Math.max(0, Math.round(sl.top) - 6);
	for (const [name, clip] of [["footer-left", { x: 0, y, width: 720, height: 40 }], ["footer-right", { x: 720, y, width: 720, height: 40 }], ["header", { x: 0, y: 0, width: 1440, height: 190 }]]) {
		const r = await send("Page.captureScreenshot", { format: "png", clip: { ...clip, scale: 1 } });
		writeFileSync(join(OUT, `zoom-${th}-${name}.png`), Buffer.from(r.result.data, "base64"));
	}
}
const shape = await ev(`(async()=>{const out={};const keys=(o,d=0)=>{if(Array.isArray(o))return ['array('+o.length+')', o[0]&&typeof o[0]==='object'&&d<2?keys(o[0],d+1):typeof o[0]];if(o&&typeof o==='object'){const r={};for(const k of Object.keys(o).slice(0,40))r[k]=d<2?keys(o[k],d+1):typeof o[k];return r}return typeof o};
for(const p of ['state','stats?range=7d','doctor','devices','wip','accounts','projects','agent','version','conflicts','settings']){try{const r=await fetch('/api/'+p,{credentials:'same-origin'});out[p]=r.ok?keys(await r.json()):r.status}catch(e){out[p]=String(e)}}return JSON.stringify(out)})()`);
writeFileSync(join(OUT, "api-shape.json"), JSON.stringify(JSON.parse(shape), null, 1));
console.log("footer close-ups and api-shape.json in", OUT);
close(); chrome.kill();
process.exit(0);

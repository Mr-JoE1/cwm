(() => {
	const vis = (e) => { const r = e.getBoundingClientRect(); const s = getComputedStyle(e); return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none" && +s.opacity > 0.05 && !e.closest("[hidden]"); };
	const parse = (c) => { const m = c.match(/rgba?\(([^)]+)\)/); if (!m) return null; const p = m[1].split(/[ ,/]+/).filter(Boolean).map(Number); return { r: p[0], g: p[1], b: p[2], a: p[3] ?? 1 }; };
	const lum = ({ r, g, b }) => { const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }; return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b); };
	const blend = (fg, bg) => ({ r: fg.r * fg.a + bg.r * (1 - fg.a), g: fg.g * fg.a + bg.g * (1 - fg.a), b: fg.b * fg.a + bg.b * (1 - fg.a), a: 1 });
	const rootBg = parse(getComputedStyle(document.body).backgroundColor);
	const pageBg = rootBg && rootBg.a > 0 ? rootBg : parse(getComputedStyle(document.documentElement).backgroundColor) || { r: 0, g: 0, b: 0, a: 1 };
	const effBg = (e) => { const stack = []; for (let n = e; n && n.nodeType === 1; n = n.parentElement) { const c = parse(getComputedStyle(n).backgroundColor); if (c && c.a > 0) { stack.push(c); if (c.a >= 1) break; } } let bg = pageBg.a >= 1 ? pageBg : { r: 0, g: 0, b: 0, a: 1 }; for (let i = stack.length - 1; i >= 0; i--) bg = blend(stack[i], bg); return bg; };
	const ratio = (a, b) => { const l1 = lum(a), l2 = lum(b); return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05); };
	const label = (e) => (e.id ? "#" + e.id : e.tagName.toLowerCase() + (e.className && typeof e.className === "string" ? "." + e.className.trim().split(/\s+/).slice(0, 2).join(".") : "")) + " «" + (e.innerText || e.getAttribute("aria-label") || e.title || "").trim().replace(/\s+/g, " ").slice(0, 40) + "»";
	const scope = document.querySelector("dialog[open]") || document.body;
	const all = [...scope.querySelectorAll("*")].filter(vis);
	const W = innerWidth;
	// text nodes leaf elements
	const textEls = all.filter((e) => [...e.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim().length > 1));
	const lowContrast = []; const fontSizes = {}; const families = new Set(); const weights = {};
	for (const e of textEls) {
		const s = getComputedStyle(e); const fs = parseFloat(s.fontSize);
		fontSizes[fs] = (fontSizes[fs] || 0) + 1; families.add(s.fontFamily.split(",")[0].trim()); weights[s.fontWeight] = (weights[s.fontWeight] || 0) + 1;
		const fg = parse(s.color); if (!fg) continue; const bg = effBg(e); const c = ratio(blend(fg, bg), bg);
		const large = fs >= 24 || (fs >= 18.66 && +s.fontWeight >= 700);
		if (c < (large ? 3 : 4.5)) lowContrast.push({ el: label(e), ratio: +c.toFixed(2), fs, color: s.color, bg: `rgb(${bg.r | 0},${bg.g | 0},${bg.b | 0})` });
	}
	const interactive = all.filter((e) => e.matches("button,a[href],input,select,textarea,[role=tab],[role=button],[role=option],[tabindex]:not([tabindex='-1']),summary"));
	const small = interactive.map((e) => ({ e, r: e.getBoundingClientRect() })).filter(({ r }) => r.width < 24 || r.height < 24).map(({ e, r }) => label(e) + ` ${r.width | 0}x${r.height | 0}`);
	const under44 = interactive.map((e) => ({ e, r: e.getBoundingClientRect() })).filter(({ r }) => r.height < 44 || r.width < 44).length;
	const noName = interactive.filter((e) => !(e.innerText || "").trim() && !e.getAttribute("aria-label") && !e.getAttribute("aria-labelledby") && !e.title && !(e.labels && e.labels.length) && !e.placeholder).map(label);
	const overflowX = all.filter((e) => { const r = e.getBoundingClientRect(); return r.right > W + 1 && getComputedStyle(e).position !== "fixed"; }).slice(0, 12).map((e) => label(e) + " right=" + (e.getBoundingClientRect().right | 0));
	const truncated = all.filter((e) => { const s = getComputedStyle(e); return (s.textOverflow === "ellipsis" || s.overflow === "hidden") && e.scrollWidth > e.clientWidth + 2 && (e.innerText || "").trim(); }).slice(0, 15).map((e) => label(e));
	const headings = [...scope.querySelectorAll("h1,h2,h3,h4,h5,h6")].filter(vis).map((h) => h.tagName + ": " + h.innerText.trim().replace(/\s+/g, " ").slice(0, 60));
	const panel = document.querySelector(".panel:not([hidden])");
	const emptyStates = [...(panel || scope).querySelectorAll(".empty,[class*=empty],[class*=placeholder]")].filter(vis).map((e) => e.innerText.trim().slice(0, 120));
	const panelText = panel ? panel.innerText.replace(/\n{2,}/g, "\n").slice(0, 2500) : "";
	const statusbar = document.querySelector(".statusline,.sl,[class*=status]");
	return JSON.stringify({
		vw: W, docW: document.documentElement.scrollWidth, docH: document.documentElement.scrollHeight,
		hOverflow: document.documentElement.scrollWidth > W + 1, overflowX,
		theme: document.documentElement.dataset.theme, activeTab: document.querySelector("[role=tab][aria-selected=true]")?.dataset.tab,
		fontSizes, families: [...families], weights, lowContrastCount: lowContrast.length, lowContrast: lowContrast.slice(0, 25),
		interactiveCount: interactive.length, smallTargets: small.slice(0, 30), smallTargetCount: small.length, under44,
		noName, truncated, headings, emptyStates, panelText,
		dialogOpen: document.querySelector("dialog[open]")?.id || null,
		dialogText: document.querySelector("dialog[open]")?.innerText.slice(0, 1500) || null,
		bodyFont: getComputedStyle(document.body).fontFamily, bodyFs: getComputedStyle(document.body).fontSize,
	});
})()

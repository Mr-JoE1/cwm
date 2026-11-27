// cwm live background (v0.7): a slow particle network with a drifting scan
// band and rare glitch slices, drawn on one fixed canvas behind the page.
// Budget rules: at most 30 frames/s, devicePixelRatio capped at 2, paused
// while the tab is hidden, frozen on its last frame after 30s without user
// input (any pointer/key/scroll/touch resumes it — an idle dashboard draws
// no frames at all), one static frame (no motion) under
// prefers-reduced-motion, and fully off when <html data-bg="off">. Colours
// come from the theme tokens (--bg-node / --bg-link / --bg-glitch).
"use strict";

(function () {
	const canvas = document.getElementById("bg");
	if (!canvas || !canvas.getContext) return;
	const ctx = canvas.getContext("2d");
	const root = document.documentElement;
	const reduce = window.matchMedia("(prefers-reduced-motion: reduce)");
	const FRAME_MS = 1000 / 30;
	const LINK = 140; // px: max distance at which two nodes are joined
	const BUCKETS = 4; // line alpha buckets → 4 strokes per frame, not one per line
	const IDLE_MS = 30000; // freeze after this long without input

	let w = 0, h = 0, dpr = 1;
	let nodes = [];
	let raf = 0, last = 0, scanY = 0;
	let pal = null;
	let glitchUntil = 0, nextGlitch = 0, slices = [];
	let lastInput = performance.now();

	function readPalette() {
		const cs = getComputedStyle(root);
		const v = (name, dflt) => (cs.getPropertyValue(name).trim() || dflt);
		pal = {
			node: v("--bg-node", "0, 255, 156"),
			link: v("--bg-link", "0, 229, 255"),
			glitch: v("--bg-glitch", "255, 60, 216"),
			alpha: parseFloat(v("--bg-alpha", "1")) || 1,
		};
	}

	function seed() {
		const count = Math.max(24, Math.min(80, Math.round((w * h) / 21000)));
		nodes = [];
		for (let i = 0; i < count; i++) {
			const a = Math.random() * Math.PI * 2;
			const speed = 0.08 + Math.random() * 0.22; // px per frame at 30fps
			nodes.push({
				x: Math.random() * w, y: Math.random() * h,
				vx: Math.cos(a) * speed, vy: Math.sin(a) * speed,
				r: Math.random() < 0.12 ? 1.9 : 1.1,
			});
		}
	}

	function resize() {
		dpr = Math.min(window.devicePixelRatio || 1, 2);
		w = window.innerWidth;
		h = window.innerHeight;
		canvas.width = Math.round(w * dpr);
		canvas.height = Math.round(h * dpr);
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		if (!nodes.length) seed();
		else for (const n of nodes) { n.x = Math.min(n.x, w); n.y = Math.min(n.y, h); }
	}

	function step() {
		for (const n of nodes) {
			n.x += n.vx;
			n.y += n.vy;
			if (n.x < -10) n.x = w + 10; else if (n.x > w + 10) n.x = -10;
			if (n.y < -10) n.y = h + 10; else if (n.y > h + 10) n.y = -10;
		}
		scanY = (scanY + 0.9) % (h + 160);
	}

	function draw(now) {
		const a = pal.alpha;
		ctx.clearRect(0, 0, w, h);

		// links, bucketed by distance so each bucket is a single stroke
		const paths = [];
		for (let b = 0; b < BUCKETS; b++) paths.push(new Path2D());
		const max2 = LINK * LINK;
		for (let i = 0; i < nodes.length; i++) {
			const p = nodes[i];
			for (let j = i + 1; j < nodes.length; j++) {
				const q = nodes[j];
				const dx = p.x - q.x, dy = p.y - q.y;
				const d2 = dx * dx + dy * dy;
				if (d2 > max2) continue;
				const b = Math.min(BUCKETS - 1, Math.floor((d2 / max2) * BUCKETS));
				paths[b].moveTo(p.x, p.y);
				paths[b].lineTo(q.x, q.y);
			}
		}
		ctx.lineWidth = 1;
		ctx.strokeStyle = "rgb(" + pal.link + ")";
		for (let b = 0; b < BUCKETS; b++) {
			ctx.globalAlpha = a * 0.16 * (1 - b / BUCKETS);
			ctx.stroke(paths[b]);
		}

		// nodes
		ctx.fillStyle = "rgb(" + pal.node + ")";
		ctx.globalAlpha = a * 0.55;
		const dots = new Path2D();
		for (const n of nodes) {
			dots.moveTo(n.x + n.r, n.y);
			dots.arc(n.x, n.y, n.r, 0, Math.PI * 2);
		}
		ctx.fill(dots);

		// scan band
		ctx.globalAlpha = a * 0.05;
		ctx.fillRect(0, scanY - 160, w, 1);
		ctx.globalAlpha = a * 0.025;
		ctx.fillRect(0, scanY - 158, w, 36);

		// rare glitch slices
		if (now && now > nextGlitch) {
			glitchUntil = now + 140 + Math.random() * 120;
			nextGlitch = now + 7000 + Math.random() * 9000;
			slices = [];
			const k = 2 + Math.floor(Math.random() * 3);
			for (let i = 0; i < k; i++) {
				slices.push({ y: Math.random() * h, hgt: 1 + Math.random() * 3, x: Math.random() * w * 0.6, len: 60 + Math.random() * w * 0.35 });
			}
		}
		if (now && now < glitchUntil) {
			ctx.fillStyle = "rgb(" + pal.glitch + ")";
			ctx.globalAlpha = a * 0.28;
			for (const s of slices) ctx.fillRect(s.x, s.y, s.len, s.hgt);
			ctx.fillStyle = "rgb(" + pal.link + ")";
			ctx.globalAlpha = a * 0.18;
			for (const s of slices) ctx.fillRect(s.x + 6, s.y + s.hgt + 1, s.len * 0.6, 1);
		}
		ctx.globalAlpha = 1;
	}

	function frame(now) {
		if (now - lastInput > IDLE_MS) { raf = 0; root.setAttribute("data-idle", ""); return; } // idle: keep the last frame, stop drawing (CSS stops the heading caret too)
		raf = requestAnimationFrame(frame);
		if (now - last < FRAME_MS - 2) return; // ~30fps cap on a 60Hz+ display
		last = now;
		step();
		draw(now);
	}

	function on() { return root.getAttribute("data-bg") !== "off"; }
	function still() { return reduce.matches || root.getAttribute("data-fx") === "subtle"; }

	function stop() {
		if (raf) cancelAnimationFrame(raf);
		raf = 0;
	}

	function sync() {
		stop();
		if (!on()) {
			ctx.clearRect(0, 0, w, h);
			return;
		}
		if (still()) {
			draw(0); // one still frame: the look without the motion (reduced motion, Effects: subtle)
			return;
		}
		if (!document.hidden) {
			lastInput = performance.now(); // becoming visible counts as activity
			nextGlitch = performance.now() + 4000;
			raf = requestAnimationFrame(frame);
		}
	}

	function wake() {
		lastInput = performance.now();
		if (root.hasAttribute("data-idle")) root.removeAttribute("data-idle");
		if (!raf && on() && !still() && !document.hidden) raf = requestAnimationFrame(frame);
	}
	for (const type of ["pointermove", "pointerdown", "keydown", "wheel", "touchstart", "scroll"]) {
		window.addEventListener(type, wake, { passive: true, capture: true });
	}

	let resizeTimer = 0;
	window.addEventListener("resize", () => {
		clearTimeout(resizeTimer);
		resizeTimer = setTimeout(() => { resize(); if (!raf) sync(); }, 150);
	});
	document.addEventListener("visibilitychange", sync);
	if (reduce.addEventListener) reduce.addEventListener("change", sync);
	new MutationObserver(() => { readPalette(); sync(); })
		.observe(root, { attributes: true, attributeFilter: ["data-theme", "data-bg", "data-fx"] });

	readPalette();
	resize();
	sync();
})();

// cwm pre-paint (v0.7): loaded synchronously in <head>, before the
// stylesheet applies, so the first frame already has the right theme — no
// light/dark flash. It is a separate file because the CSP (D24) allows no
// inline scripts. Preferences: localStorage "cwm-theme" = dark | light |
// system (default), "cwm-fx" = off | subtle | full (default; v0.9.1 F10,
// read from the old "cwm-bg" on/off when unset). data-bg stays the motion
// contract bg.js reads: off only when effects are off.
(function () {
	var pref = "system";
	var fx = "full";
	try {
		pref = localStorage.getItem("cwm-theme") || "system";
		fx = localStorage.getItem("cwm-fx") || (localStorage.getItem("cwm-bg") === "off" ? "off" : "full");
	} catch (e) { /* storage disabled: defaults */ }
	if (fx !== "off" && fx !== "subtle") fx = "full";
	if (pref !== "dark" && pref !== "light") pref = "system";
	var light = pref === "light" ||
		(pref === "system" && !!window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches);
	var root = document.documentElement;
	root.setAttribute("data-theme", light ? "light" : "dark");
	root.setAttribute("data-theme-pref", pref);
	root.setAttribute("data-fx", fx);
	root.setAttribute("data-bg", fx === "off" ? "off" : "on");
})();

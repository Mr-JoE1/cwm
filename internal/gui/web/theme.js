// cwm pre-paint (v0.7): loaded synchronously in <head>, before the
// stylesheet applies, so the first frame already has the right theme — no
// light/dark flash. It is a separate file because the CSP (D24) allows no
// inline scripts. Preferences: localStorage "cwm-theme" = dark | light |
// system (default), "cwm-bg" = on (default) | off.
(function () {
	var pref = "system";
	var bg = "on";
	try {
		pref = localStorage.getItem("cwm-theme") || "system";
		bg = localStorage.getItem("cwm-bg") || "on";
	} catch (e) { /* storage disabled: defaults */ }
	if (pref !== "dark" && pref !== "light") pref = "system";
	var light = pref === "light" ||
		(pref === "system" && !!window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches);
	var root = document.documentElement;
	root.setAttribute("data-theme", light ? "light" : "dark");
	root.setAttribute("data-theme-pref", pref);
	root.setAttribute("data-bg", bg === "off" ? "off" : "on");
})();

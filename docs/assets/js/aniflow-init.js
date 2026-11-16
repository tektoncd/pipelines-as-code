(() => {
  const script = document.currentScript;
  const base = script.dataset.base;
  const root = document.documentElement;
  const dark = () => root.classList.contains("dark");
  const start = () => {
    const diagrams = Aniflow.diagrams({
      root: document.getElementById("content") || document.body,
      mermaid: script.dataset.mermaid,
      dark,
      resolveImage: (src) => (src.startsWith("/icons/") ? base + src.slice(1) : null),
    });
    let wasDark = dark();
    new MutationObserver(() => {
      if (dark() === wasDark) return;
      wasDark = dark();
      diagrams.redraw();
    }).observe(root, { attributes: true, attributeFilter: ["class"] });
  };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();

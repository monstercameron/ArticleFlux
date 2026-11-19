// The desktop shell's page script. NOT application code: see bridge.go for why
// it exists, and for why it is injected by the shell instead of being served.
//
// Three jobs, and nothing else:
//   1. Report document.title, so the native title bar follows the article.
//   2. Send links that leave the app to the person's browser, never to a
//      WebView2 popup inside the app.
//   3. Receive the menu's commands and hand each to the client's own path for
//      it — the address grammar or the keyboard map — so a menu item and the
//      shortcut it mirrors cannot behave differently.
(() => {
  const ORIGIN = __ORIGIN__;
  if (location.origin !== ORIGIN || window.__articlefluxDesktop) return;

  const post = (m) => {
    try { window.chrome.webview.postMessage(m); } catch (_) { /* not in WebView2 */ }
  };

  const external = (href) => {
    try {
      const u = new URL(href, location.href);
      if (u.protocol !== "http:" && u.protocol !== "https:") return "";
      return u.origin === location.origin ? "" : u.href;
    } catch (_) {
      return "";
    }
  };

  // 1. The title. The client sets document.title when an article opens
  // (client/view/reader.go), which rewrites the <title> text node under <head>.
  let lastTitle = null;
  const sendTitle = () => {
    if (document.title !== lastTitle) {
      lastTitle = document.title;
      post("articleflux:title " + lastTitle);
    }
  };
  new MutationObserver(sendTitle).observe(document.head || document.documentElement,
    { subtree: true, childList: true, characterData: true });
  sendTitle();

  // 2a. window.open — client/platform.OpenExternal, the "open original" path.
  const nativeOpen = window.open;
  window.open = function (url, target, features) {
    const href = external(url);
    if (href) {
      post("articleflux:open " + href);
      return null;
    }
    return nativeOpen.apply(this, arguments);
  };

  // 2b. Plain <a href> to another origin, target=_blank or not. Bubble phase on
  // window, so it runs LAST: if the client handled the click itself (and
  // prevented the default, typically to call window.open above), this does
  // nothing, and the link opens once rather than twice.
  const onClick = (e) => {
    if (e.defaultPrevented || (e.button !== 0 && e.button !== 1)) return;
    const a = e.target instanceof Element ? e.target.closest("a[href]") : null;
    if (!a || a.hasAttribute("download")) return;
    const href = external(a.href);
    if (!href) return;
    e.preventDefault();
    post("articleflux:open " + href);
  };
  window.addEventListener("click", onClick);
  window.addEventListener("auxclick", onClick);

  // 3. Commands from the menu.
  const go = (path) => {
    history.pushState(null, "", path);
    window.dispatchEvent(new PopStateEvent("popstate", { state: null }));
  };
  const key = (k) => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
  };
  window.__articlefluxDesktop = Object.freeze({
    run(cmd) {
      switch (cmd) {
        case "add": {
          let p = location.pathname.replace(/\/+$/, "").replace(/\/(add|slideshow)$/, "");
          if (p === "/settings" || p.startsWith("/settings/")) p = "";
          go(p + "/add" + (p === "/search" ? location.search : ""));
          break;
        }
        case "settings":
          go("/settings/reading");
          break;
        case "refresh":
          key("r");
          break;
      }
    },
  });
})();

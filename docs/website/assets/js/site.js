/* YT Cartell Studio — site behaviour. Vanilla, no trackers. */
(function () {
  "use strict";

  var REPO = "gitkalenyuk/yt-cartell-studio";
  var RELEASES_URL = "https://github.com/" + REPO + "/releases";
  var API = "https://api.github.com/repos/" + REPO;
  var CACHE_KEY = "ytcs-releases-v2";
  var CACHE_TTL = 10 * 60 * 1000;
  var THEME_KEY = "ytcs-theme";

  var root = document.documentElement;
  var reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  var systemDark = window.matchMedia("(prefers-color-scheme: dark)");

  function $(s, el) { return (el || document).querySelector(s); }
  function $$(s, el) { return Array.prototype.slice.call((el || document).querySelectorAll(s)); }
  function store(k, v) { try { if (v === undefined) return localStorage.getItem(k); localStorage.setItem(k, v); } catch (e) { return null; } }

  /* ---------------- theme ---------------- */
  function syncPictures() {
    var saved = store(THEME_KEY);
    var media = saved === "dark" ? "all" : saved === "light" ? "not all" : "(prefers-color-scheme: dark)";
    $$("source[data-dark]").forEach(function (s) { if (s.media !== media) s.media = media; });
  }
  function applyTheme(t, animate) {
    if (animate && !reduceMotion.matches) {
      root.classList.add("theme-anim");
      clearTimeout(applyTheme.t);
      applyTheme.t = setTimeout(function () { root.classList.remove("theme-anim"); }, 320);
    }
    root.setAttribute("data-theme", t);
    var btn = $(".theme-toggle");
    if (btn) btn.setAttribute("aria-label", t === "dark" ? "Увімкнути світлу тему" : "Увімкнути темну тему");
    syncPictures();
  }
  applyTheme(root.getAttribute("data-theme") === "dark" ? "dark" : "light", false);
  var toggle = $(".theme-toggle");
  if (toggle) toggle.addEventListener("click", function () {
    var next = root.getAttribute("data-theme") === "dark" ? "light" : "dark";
    store(THEME_KEY, next);
    applyTheme(next, true);
  });
  var onSystem = function () { if (!store(THEME_KEY)) applyTheme(systemDark.matches ? "dark" : "light", true); };
  if (systemDark.addEventListener) systemDark.addEventListener("change", onSystem); else if (systemDark.addListener) systemDark.addListener(onSystem);

  /* ---------------- header ---------------- */
  var header = $(".site-header");
  var onScroll = function () { if (header) header.classList.toggle("is-scrolled", window.scrollY > 8); };
  window.addEventListener("scroll", onScroll, { passive: true });
  onScroll();

  // One orange button per view: the header button turns orange only while no other orange button is on screen.
  var headerCta = $(".header-cta");
  if ("IntersectionObserver" in window && headerCta) {
    var visible = new Set();
    var started = false;
    var syncCta = function () {
      var other = false;
      visible.forEach(function (el) { if (el.classList.contains("btn-primary")) other = true; });
      var on = started && !other;
      headerCta.classList.toggle("btn-primary", on);
      headerCta.classList.toggle("btn-secondary", !on);
    };
    var ctaIO = new IntersectionObserver(function (es) {
      es.forEach(function (e) { if (e.isIntersecting) visible.add(e.target); else visible.delete(e.target); });
      started = window.scrollY > 200;
      syncCta();
    });
    $$("main .btn").forEach(function (b) { ctaIO.observe(b); });
    window.addEventListener("scroll", function () { var s = window.scrollY > 200; if (s !== started) { started = s; syncCta(); } }, { passive: true });
    window.__ytcsSyncCta = syncCta;
  }

  /* ---------------- motion ---------------- */
  var hero = $(".hero");
  var ready = function () { if (hero) hero.classList.add("is-ready"); };
  if (reduceMotion.matches) ready();
  else {
    var fired = false;
    var go = function () { if (!fired) { fired = true; requestAnimationFrame(function () { requestAnimationFrame(ready); }); } };
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(go);
    setTimeout(go, 450);
  }

  var revealEls = $$("[data-reveal]");
  if (!("IntersectionObserver" in window) || reduceMotion.matches) {
    revealEls.forEach(function (el) { el.classList.add("is-in"); });
  } else {
    var io = new IntersectionObserver(function (es) {
      es.forEach(function (e) {
        if (e.isIntersecting) { e.target.classList.add("is-in"); io.unobserve(e.target); }
      });
    }, { rootMargin: "0px 0px -8% 0px", threshold: 0.08 });
    revealEls.forEach(function (el) { io.observe(el); });
  }

  /* ---------------- steps tabs ---------------- */
  var tabs = $$('.tabs-track [role="tab"]');
  var ind = $(".tabs-ind");
  var track = $(".tabs-track");
  function placeIndicator() {
    var cur = $('.tabs-track [aria-selected="true"]');
    if (!cur || !ind) return;
    ind.style.width = cur.offsetWidth + "px";
    ind.style.transform = "translateX(" + cur.offsetLeft + "px)";
  }
  function selectTab(tab, focus) {
    tabs.forEach(function (t) {
      var on = t === tab;
      t.setAttribute("aria-selected", on ? "true" : "false");
      t.tabIndex = on ? 0 : -1;
      var p = document.getElementById(t.getAttribute("aria-controls"));
      if (!p) return;
      if (on) {
        var was = p.hidden;
        p.hidden = false;
        if (was && !reduceMotion.matches) {
          p.classList.remove("is-entering"); void p.offsetWidth; p.classList.add("is-entering");
        }
        // panel content was hidden when the page loaded: reveal it now
        $$("[data-reveal]", p).forEach(function (el) { el.classList.add("is-in"); });
      } else p.hidden = true;
    });
    if (focus) tab.focus();
    placeIndicator();
    if (track && tab.scrollIntoView && track.scrollWidth > track.clientWidth) {
      track.scrollTo({ left: tab.offsetLeft - 24, behavior: reduceMotion.matches ? "auto" : "smooth" });
    }
  }
  if (tabs.length) {
    tabs.forEach(function (t, i) {
      t.addEventListener("click", function () { selectTab(t, false); });
      t.addEventListener("keydown", function (e) {
        var j = null;
        if (e.key === "ArrowRight") j = (i + 1) % tabs.length;
        else if (e.key === "ArrowLeft") j = (i - 1 + tabs.length) % tabs.length;
        else if (e.key === "Home") j = 0;
        else if (e.key === "End") j = tabs.length - 1;
        if (j !== null) { e.preventDefault(); selectTab(tabs[j], true); }
      });
    });
    tabs.forEach(function (t, i) { if (i > 0) { var p = document.getElementById(t.getAttribute("aria-controls")); if (p) p.hidden = true; } });
    placeIndicator();
    window.addEventListener("resize", placeIndicator);
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(placeIndicator);
    // warm the other steps' screenshots once the section is near
    var how = $("#how");
    if (how && "IntersectionObserver" in window) {
      var warm = new IntersectionObserver(function (es) {
        if (!es[0].isIntersecting) return;
        warm.disconnect();
        $$(".step-panel img", how).forEach(function (img) { img.loading = "eager"; });
      }, { rootMargin: "400px 0px" });
      warm.observe(how);
    }
  }

  /* ---------------- style wall ---------------- */
  var grid = $("#style-grid");
  var chips = $$(".chips .chip");
  var moreBtn = $("#styles-all");
  var expanded = false;
  var filter = "all";
  function collapsedCount() {
    var cols = getComputedStyle(grid).gridTemplateColumns.split(" ").filter(Boolean).length || 2;
    return cols * (cols <= 2 ? 5 : 3);
  }
  function renderStyles(animate) {
    var tiles = $$(".style", grid);
    var limit = filter === "all" && !expanded ? collapsedCount() : Infinity;
    var shown = 0, d = 0;
    tiles.forEach(function (li) {
      var match = filter === "all" || li.getAttribute("data-cat") === filter;
      var show = match && shown < limit;
      if (match) shown++;
      var wasHidden = li.hidden;
      li.hidden = !show;
      li.classList.remove("is-new");
      if (show && animate && (wasHidden || filter !== "all") && !reduceMotion.matches) {
        li.style.setProperty("--d", d++);
        void li.offsetWidth;
        li.classList.add("is-new");
      }
    });
    if (moreBtn) {
      var total = filter === "all" ? tiles.length : 0;
      moreBtn.hidden = !(filter === "all" && (expanded || shown > limit));
      moreBtn.setAttribute("aria-expanded", expanded ? "true" : "false");
      var label = moreBtn.firstChild;
      if (label && label.nodeType === 3) label.nodeValue = expanded ? "Згорнути" : "Показати всі " + total + " " + plural(total, "стиль", "стилі", "стилів");
      moreBtn.classList.toggle("is-open", expanded);
    }
  }
  if (grid) {
    chips.forEach(function (c) {
      c.addEventListener("click", function () {
        filter = c.getAttribute("data-filter");
        chips.forEach(function (x) { x.setAttribute("aria-pressed", x === c ? "true" : "false"); });
        renderStyles(true);
      });
    });
    if (moreBtn) moreBtn.addEventListener("click", function () {
      expanded = !expanded;
      renderStyles(expanded);
      if (!expanded) grid.scrollIntoView({ block: "start", behavior: reduceMotion.matches ? "auto" : "smooth" });
    });
    renderStyles(false);
    var rt;
    window.addEventListener("resize", function () { clearTimeout(rt); rt = setTimeout(function () { if (!expanded) renderStyles(false); }, 150); });
  }

  /* ---------------- copy buttons: the Mac install prompt, the example AI prompts ---------------- */
  $$(".copy-btn").forEach(function (btn) {
    var label = $(".copy-label", btn);
    var idle = label ? label.textContent : "";
    var card = btn.closest(".prompt-card");
    var status = card ? $(".copy-status", card) : null;
    var timer;
    function done(ok, msg) {
      clearTimeout(timer);
      btn.classList.toggle("is-done", ok);
      if (label) label.textContent = ok ? "Скопійовано" : idle;
      if (status) status.textContent = msg || "";
      timer = setTimeout(function () {
        btn.classList.remove("is-done");
        if (label) label.textContent = idle;
        if (status) status.textContent = "";
      }, ok ? 2600 : 6000);
    }
    function legacyCopy(text) {
      var ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "");
      ta.style.position = "fixed"; ta.style.top = "-1000px"; ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      var ok = false;
      try { ok = document.execCommand("copy"); } catch (e) { ok = false; }
      document.body.removeChild(ta);
      btn.focus();
      return ok;
    }
    function selectText(el) {
      var r = document.createRange(); r.selectNodeContents(el);
      var sel = window.getSelection(); sel.removeAllRanges(); sel.addRange(r);
    }
    btn.addEventListener("click", function () {
      var src = $(btn.getAttribute("data-copy"));
      if (!src) return;
      var text = src.textContent;
      var okMsg = btn.getAttribute("data-copied") || "Інструкцію скопійовано — встав її в агента.";
      var fallback = function () {
        if (legacyCopy(text)) done(true, okMsg);
        else { selectText(src); done(false, "Текст виділено — натисни ⌘C або Ctrl+C."); }
      };
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(function () { done(true, okMsg); }, fallback);
      } else fallback();
    });
  });

  /* ---------------- videos ----------------
     Every <article class="vid"> inside #videos is one video with its own <video> markup
     (poster + optional subtitles track). An entry marked data-probe stays hidden until its
     file is published in docs/media/; the first visible entry becomes the large one. */
  (function () {
    var list = $("#videos");
    if (!list) return;
    function lead() {
      var first = null;
      $$(".vid", list).forEach(function (el) {
        if (!el.hidden && !first) first = el;
        el.classList.remove("is-lead");
      });
      if (first) first.classList.add("is-lead");
    }
    $$(".vid[data-probe]", list).forEach(function (item) {
      var v = $("video", item);
      if (!v) return;
      v.addEventListener("loadedmetadata", function () {
        if (v.getAttribute("data-poster")) v.poster = v.getAttribute("data-poster");
        item.hidden = false;
        $$("[data-reveal]", item).forEach(function (el) { el.classList.add("is-in"); });
        lead();
      }, { once: true });
      v.preload = "metadata";
      v.load();
    });
    lead();
  })();

  /* ---------------- guide pages: the section in view ---------------- */
  (function () {
    var links = $$(".g-toc a[href^='#']");
    if (!links.length || !("IntersectionObserver" in window)) return;
    var byId = {};
    links.forEach(function (a) { byId[a.getAttribute("href").slice(1)] = a; });
    var heads = $$(".g-article h2[id]").filter(function (h) { return byId[h.id]; });
    var current = null;
    function mark(id) {
      if (id === current) return;
      current = id;
      links.forEach(function (a) { a.classList.toggle("is-current", a === byId[id]); });
    }
    function update() {
      var best = heads.length ? heads[0].id : null;
      heads.forEach(function (h) { if (h.getBoundingClientRect().top < window.innerHeight * 0.4) best = h.id; });
      if (best) mark(best);
    }
    var tio = new IntersectionObserver(update, { rootMargin: "0px 0px -55% 0px", threshold: [0, 1] });
    heads.forEach(function (h) { tio.observe(h); });
    window.addEventListener("scroll", update, { passive: true });
    update();
  })();

  /* ---------------- «no Mac build yet» notes ----------------
     An element with data-mac-pending="X.Y.Z" stays visible only while X.Y.Z is the newest
     release and that release has no macOS build. */
  function macPending(version, hasMac) {
    $$("[data-mac-pending]").forEach(function (el) {
      var v = el.getAttribute("data-mac-pending");
      el.hidden = hasMac || (!!v && v !== version);
    });
  }

  // Only the landing page has the download block: the release lookup below is for it.
  if (!$("#dl-version")) {
    if ($("[data-mac-pending]") && window.fetch) {
      fetch(API + "/releases/latest", { headers: { "Accept": "application/vnd.github+json" } })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (j) {
          if (!j) return;
          var names = (j.assets || []).map(function (a) { return a.name; }).join(" ");
          macPending(String(j.tag_name || "").replace(/^v/i, ""), /macos-(arm64|x64|universal)\.zip/i.test(names));
        })
        .catch(function () {});
    }
    return;
  }

  /* ---------------- OS detection ---------------- */
  var ua = navigator.userAgent || "";
  var plat = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";
  var os = "other";
  if (/Android|iPhone|iPad|iPod/i.test(ua) || (/Mac/i.test(plat) && navigator.maxTouchPoints > 1)) os = "mobile";
  else if (/Win/i.test(plat) || /Windows/i.test(ua)) os = "win";
  else if (/Mac/i.test(plat) || /Mac OS X/i.test(ua)) os = "mac-arm";
  else if (/Linux|X11|CrOS/i.test(plat + ua)) os = "linux";

  // Chromium on a Mac can tell Apple Silicon from Intel; Safari and Firefox cannot, so Apple Silicon is the default.
  if (os === "mac-arm" && navigator.userAgentData && navigator.userAgentData.getHighEntropyValues) {
    navigator.userAgentData.getHighEntropyValues(["architecture"]).then(function (v) {
      if (v && v.architecture === "x86") { os = "mac-x64"; if (last) render(last[0], last[1], last[2]); }
    }).catch(function () {});
  }

  /* ---------------- releases ---------------- */
  var nf = new Intl.NumberFormat("uk-UA");
  function plural(n, one, few, many) {
    var m10 = n % 10, m100 = n % 100;
    if (m10 === 1 && m100 !== 11) return one;
    if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
    return many;
  }
  function mb(bytes) {
    var v = bytes / 1048576;
    return new Intl.NumberFormat("uk-UA", { maximumFractionDigits: v >= 10 ? 0 : 1 }).format(v) + " МБ";
  }
  function dateUk(iso) {
    try { return new Intl.DateTimeFormat("uk-UA", { day: "numeric", month: "long", year: "numeric" }).format(new Date(iso)); } catch (e) { return ""; }
  }
  var PATTERNS = { "win": /windows-x64\.exe$/i, "mac-arm": /macos-arm64\.zip$/i, "mac-x64": /macos-x64\.zip$/i };
  var LABELS = { "win": "Завантажити для Windows", "mac-arm": "Завантажити для macOS", "mac-x64": "Завантажити для macOS" };
  var SYS = { "win": "Windows 10/11, 64-біт", "mac-arm": "macOS · Apple Silicon", "mac-x64": "macOS · Intel" };
  var EXT = { "win": ".exe", "mac-arm": ".zip", "mac-x64": ".zip" };

  function highlight(key) {
    $$(".dl-row").forEach(function (row) {
      var on = row.getAttribute("data-os") === key;
      row.classList.toggle("is-you", on);
      var you = $(".dl-you", row); if (you) you.hidden = !on;
      var b = $(".btn", row);
      if (b) { b.classList.toggle("btn-primary", on); b.classList.toggle("btn-secondary", !on); }
    });
    if (window.__ytcsSyncCta) window.__ytcsSyncCta();
  }

  var heroDl = $("#hero-dl"), heroLabel = $("#hero-dl-label"), heroMeta = $("#hero-meta-text"), heroCount = $("#hero-count");

  function setupHero(rel) {
    if (!heroDl) return;
    if (os === "mobile") {
      heroLabel.textContent = "Завантажити студію";
      heroDl.href = "#download";
      heroMeta.textContent = "Програма для комп'ютера: Windows 10/11 і macOS";
      return;
    }
    if (os === "linux" || os === "other") {
      heroLabel.textContent = "Завантажити студію";
      heroDl.href = "#download";
      heroMeta.textContent = "Є збірки для Windows і macOS";
      return;
    }
    heroLabel.textContent = LABELS[os];
    var a = rel && rel.assets && rel.assets[os];
    var fb = !a && rel && rel.fallback && rel.fallback[os];
    if (a) {
      heroDl.href = a.url;
      heroMeta.textContent = "Версія " + rel.version + " · " + mb(a.size) + " · " + SYS[os];
    } else if (fb) {
      // the newest release has no build for this Mac yet: offer the newest one that has
      heroDl.href = fb.url;
      heroMeta.textContent = "Версія " + fb.version + " · " + mb(fb.size) + " · " + SYS[os] + " (збірки " + rel.version + " для Mac ще немає)";
    } else if (rel) {
      heroLabel.textContent = "Усі файли версії " + rel.version;
      heroDl.href = rel.url;
      heroMeta.textContent = SYS[os] + " — збірка ще готується";
    } else {
      heroDl.href = RELEASES_URL;
      heroMeta.textContent = SYS[os];
    }
  }

  var last = null;
  function render(rel, total, state) {
    last = [rel, total, state];
    setupHero(rel);
    highlight(os === "win" || os === "mac-arm" || os === "mac-x64" ? os : "");
    var status = $("#dl-status"), ver = $("#dl-version");
    Object.keys(PATTERNS).forEach(function (key) {
      var row = $('.dl-row[data-os="' + key + '"]');
      if (!row) return;
      var a = rel && rel.assets[key], btn = $(".btn", row), meta = $(".meta", row);
      var fb = !a && rel && rel.fallback && rel.fallback[key];
      var txt = btn.lastChild;
      if (txt && txt.nodeType === 3 && rel) txt.nodeValue = a || fb ? "Завантажити" : "Сторінка релізу";
      if (a || fb) {
        var f = a || fb;
        btn.href = f.url;
        btn.setAttribute("download", "");
        btn.setAttribute("aria-label", "Завантажити " + f.name + ", " + mb(f.size));
        meta.textContent = f.name + " · " + mb(f.size) + (fb ? " · версії " + rel.version + " для Mac ще немає" : "");
      } else if (rel) {
        btn.href = rel.url;
        btn.removeAttribute("download");
        meta.textContent = "Збірка ще готується — з'явиться на сторінці релізу " + rel.version;
      }
    });
    if (rel) macPending(rel.version, !!(rel.assets["mac-arm"] || rel.assets["mac-x64"]));
    if (rel) {
      ver.textContent = "Версія " + rel.version + (rel.date ? " · " + dateUk(rel.date) : "");
    } else if (state === "none") {
      ver.textContent = "Перша версія от-от з'явиться";
    } else {
      ver.textContent = "Свіжа версія — на сторінці релізів GitHub";
    }
    var countText = "";
    if (typeof total === "number" && total > 0) countText = "Завантажено " + nf.format(total) + " " + plural(total, "раз", "рази", "разів");
    if (status) {
      if (countText) status.textContent = countText;
      else if (state === "none") status.textContent = "Файли будуть на сторінці релізів.";
      else if (state === "error") status.textContent = "Не вдалося отримати дані — файли є на сторінці релізів.";
      else status.textContent = "";
    }
    if (heroCount) {
      heroCount.hidden = !countText;
      if (countText) heroCount.innerHTML = "Завантажено <strong>" + nf.format(total) + "</strong> " + plural(total, "раз", "рази", "разів");
    }
  }

  function parseLatest(j) {
    var assets = {};
    (j.assets || []).forEach(function (a) {
      Object.keys(PATTERNS).forEach(function (k) {
        if (PATTERNS[k].test(a.name) && !assets[k]) assets[k] = { name: a.name, size: a.size, url: a.browser_download_url };
      });
    });
    return { version: String(j.tag_name || j.name || "").replace(/^v/i, ""), date: j.published_at, url: j.html_url || RELEASES_URL, assets: assets };
  }
  function sumDownloads(list) {
    var n = 0;
    // only the programs themselves count, not checksum files
    (list || []).forEach(function (r) { (r.assets || []).forEach(function (a) { if (/\.(exe|zip|dmg|pkg|msi)$/i.test(a.name)) n += a.download_count || 0; }); });
    return n;
  }
  // newest published release per system — a Mac build can come out later than the Windows one
  function newestBuilds(list) {
    var out = {};
    (list || []).forEach(function (r) {
      if (r.draft || r.prerelease) return;
      (r.assets || []).forEach(function (a) {
        Object.keys(PATTERNS).forEach(function (k) {
          if (!out[k] && PATTERNS[k].test(a.name)) out[k] = { name: a.name, size: a.size, url: a.browser_download_url, version: String(r.tag_name || "").replace(/^v/i, "") };
        });
      });
    });
    return out;
  }
  function getJSON(url) {
    return fetch(url, { headers: { "Accept": "application/vnd.github+json" } }).then(function (r) {
      if (!r.ok) { var e = new Error("HTTP " + r.status); e.status = r.status; throw e; }
      return r.json();
    });
  }

  render(null, null, "loading");
  var cached = null;
  try { cached = JSON.parse(store(CACHE_KEY) || "null"); } catch (e) { cached = null; }
  if (cached && cached.rel) render(cached.rel, cached.total, "ok");
  if (cached && Date.now() - cached.t < CACHE_TTL) return;

  if (!window.fetch) { if (!cached) render(null, null, "error"); return; }
  Promise.all([
    getJSON(API + "/releases/latest").then(parseLatest).catch(function (e) { if (e.status === 404) return "none"; throw e; }),
    getJSON(API + "/releases?per_page=100").catch(function () { return null; })
  ]).then(function (res) {
    var rel = res[0] === "none" ? null : res[0];
    var total = res[1] ? sumDownloads(res[1]) : null;
    if (rel && res[1]) rel.fallback = newestBuilds(res[1]);
    if (rel) {
      store(CACHE_KEY, JSON.stringify({ t: Date.now(), rel: rel, total: total }));
      render(rel, total, "ok");
    } else if (!cached) {
      render(null, total, "none");
    }
  }).catch(function () {
    if (!cached) render(null, null, "error");
  });
})();

/* =========================================================================
   TZRO DOCS — CLIENT-SIDE INTERACTIVITY
   Version switching (v1 vs v2), Scroll-spy, sidebar, copy buttons, mobile drawer
   ========================================================================= */

document.addEventListener("DOMContentLoaded", () => {
  // =========================================================================
  // VERSION SWITCHER LOGIC (v2 Current vs v1 Legacy)
  // =========================================================================
  const versionSelect = document.getElementById("docs-version-select");
  const sidebarNavV2 = document.getElementById("sidebar-nav-v2");
  const sidebarNavV1 = document.getElementById("sidebar-nav-v1");
  const contentV2 = document.getElementById("docs-content-v2");
  const contentV1 = document.getElementById("docs-content-v1");
  const tocV2 = document.getElementById("toc-v2");
  const tocV1 = document.getElementById("toc-v1");
  const switchToV2Link = document.getElementById("switch-to-v2-link");

  let currentVersion = "v2";

  // Elements for scroll-spy
  let activeTocLinks = [];
  let activeSidebarLinks = [];
  let sectionHeadings = [];
  let sidebarSections = [];

  function refreshScrollSpyElements() {
    activeTocLinks = Array.from(document.querySelectorAll(
      currentVersion === "v2" ? "#toc-v2 .docs-toc-link" : "#toc-v1 .docs-toc-link"
    ));
    activeSidebarLinks = Array.from(document.querySelectorAll(
      currentVersion === "v2" ? "#sidebar-nav-v2 .docs-sidebar-link" : "#sidebar-nav-v1 .docs-sidebar-link"
    ));

    sectionHeadings = [];
    activeTocLinks.forEach((link) => {
      const targetId = link.getAttribute("href")?.replace("#", "");
      if (targetId) {
        const el = document.getElementById(targetId);
        if (el) sectionHeadings.push({ id: targetId, el, link });
      }
    });

    sidebarSections = [];
    activeSidebarLinks.forEach((link) => {
      const targetId = link.getAttribute("href")?.replace("#", "");
      if (targetId) {
        const el = document.getElementById(targetId);
        if (el) sidebarSections.push({ id: targetId, el, link });
      }
    });

    updateTocHighlight();
    updateSidebarHighlight();
  }

  function setDocsVersion(version, updateUrl = true) {
    if (version !== "v1" && version !== "v2") {
      version = "v2";
    }

    currentVersion = version;

    if (versionSelect && versionSelect.value !== version) {
      versionSelect.value = version;
    }

    // Toggle active classes on content & navigation
    if (version === "v2") {
      sidebarNavV2?.classList.add("active");
      sidebarNavV1?.classList.remove("active");
      contentV2?.classList.add("active");
      contentV1?.classList.remove("active");
      tocV2?.classList.add("active");
      tocV1?.classList.remove("active");
    } else {
      sidebarNavV2?.classList.remove("active");
      sidebarNavV1?.classList.add("active");
      contentV2?.classList.remove("active");
      contentV1?.classList.add("active");
      tocV2?.classList.remove("active");
      tocV1?.classList.add("active");
    }

    // Persist choice in session storage
    try {
      sessionStorage.setItem("tzro_docs_version", version);
    } catch (e) {
      // Ignore storage errors in restricted contexts
    }

    // Update URL parameter if requested
    if (updateUrl) {
      const url = new URL(window.location.href);
      if (version === "v1") {
        url.searchParams.set("v", "v1");
      } else {
        url.searchParams.delete("v"); // v2 is default clean URL
      }
      history.replaceState(null, "", url.toString());
    }

    refreshScrollSpyElements();
  }

  // Handle version dropdown change
  if (versionSelect) {
    versionSelect.addEventListener("change", (e) => {
      const selected = e.target.value;
      setDocsVersion(selected, true);
      window.scrollTo({ top: 0, behavior: "smooth" });
    });
  }

  // Handle in-page banner link to jump to v2
  if (switchToV2Link) {
    switchToV2Link.addEventListener("click", (e) => {
      e.preventDefault();
      setDocsVersion("v2", true);
      window.scrollTo({ top: 0, behavior: "smooth" });
    });
  }

  // Detect initial version from URL query param or hash
  const urlParams = new URLSearchParams(window.location.search);
  const versionParam = urlParams.get("v");

  if (versionParam === "v1") {
    setDocsVersion("v1", false);
  } else {
    // Check if initial hash belongs to v1 content
    const hash = window.location.hash.replace("#", "");
    if (hash) {
      const v1Element = contentV1?.querySelector(`#${hash}`);
      const v2Element = contentV2?.querySelector(`#${hash}`);
      if (v1Element && !v2Element) {
        setDocsVersion("v1", false);
      } else {
        setDocsVersion("v2", false);
      }
    } else {
      setDocsVersion("v2", false);
    }
  }

  // =========================================================================
  // SCROLL-SPY — Right-hand TOC highlighting
  // =========================================================================
  function updateTocHighlight() {
    const scrollY = window.scrollY + 120;

    let activeId = null;
    for (let i = sectionHeadings.length - 1; i >= 0; i--) {
      if (sectionHeadings[i].el.offsetTop <= scrollY) {
        activeId = sectionHeadings[i].id;
        break;
      }
    }

    activeTocLinks.forEach((link) => {
      const targetId = link.getAttribute("href")?.replace("#", "");
      link.classList.toggle("active", targetId === activeId);
    });
  }

  // =========================================================================
  // SCROLL-SPY — Left sidebar active state
  // =========================================================================
  function updateSidebarHighlight() {
    const scrollY = window.scrollY + 120;

    let activeId = null;
    for (let i = sidebarSections.length - 1; i >= 0; i--) {
      if (sidebarSections[i].el.offsetTop <= scrollY) {
        activeId = sidebarSections[i].id;
        break;
      }
    }

    activeSidebarLinks.forEach((link) => {
      const targetId = link.getAttribute("href")?.replace("#", "");
      link.classList.toggle("active", targetId === activeId);
    });
  }

  // Unified scroll handler
  let scrollTicking = false;
  window.addEventListener("scroll", () => {
    if (!scrollTicking) {
      requestAnimationFrame(() => {
        updateTocHighlight();
        updateSidebarHighlight();
        scrollTicking = false;
      });
      scrollTicking = true;
    }
  });

  // =========================================================================
  // SIDEBAR SECTION COLLAPSE / EXPAND
  // =========================================================================
  document.querySelectorAll(".docs-sidebar-group-title").forEach((title) => {
    title.addEventListener("click", () => {
      title.parentElement.classList.toggle("collapsed");
    });
  });

  // =========================================================================
  // MOBILE SIDEBAR TOGGLE
  // =========================================================================
  const sidebar = document.querySelector(".docs-sidebar");
  const mobileToggle = document.querySelector(".docs-mobile-toggle");
  const backdrop = document.querySelector(".docs-sidebar-backdrop");

  function openSidebar() {
    sidebar?.classList.add("open");
    backdrop?.classList.add("visible");
  }

  function closeSidebar() {
    sidebar?.classList.remove("open");
    backdrop?.classList.remove("visible");
  }

  mobileToggle?.addEventListener("click", () => {
    if (sidebar?.classList.contains("open")) {
      closeSidebar();
    } else {
      openSidebar();
    }
  });

  backdrop?.addEventListener("click", closeSidebar);

  // Close sidebar when any sidebar link is clicked (mobile)
  document.addEventListener("click", (e) => {
    const link = e.target.closest(".docs-sidebar-link");
    if (link && window.innerWidth <= 1024) {
      closeSidebar();
    }
  });

  // =========================================================================
  // CODE BLOCK COPY-TO-CLIPBOARD
  // =========================================================================
  document.querySelectorAll(".docs-code-copy").forEach((btn) => {
    btn.addEventListener("click", () => {
      const codeBlock = btn.closest(".docs-code-block");
      const code = codeBlock?.querySelector("code");
      if (!code) return;

      const text = code.textContent || "";
      navigator.clipboard.writeText(text).then(() => {
        btn.classList.add("copied");
        const label = btn.querySelector(".copy-label");
        const originalText = label?.textContent;
        if (label) label.textContent = "Copied!";

        setTimeout(() => {
          btn.classList.remove("copied");
          if (label) label.textContent = originalText || "Copy";
        }, 2000);
      });
    });
  });

  // =========================================================================
  // SMOOTH SCROLL for anchor links (handling cross-version anchors)
  // =========================================================================
  document.addEventListener("click", (e) => {
    const anchor = e.target.closest('a[href^="#"]');
    if (!anchor) return;

    const targetId = anchor.getAttribute("href")?.replace("#", "");
    if (!targetId) return;

    // Check if target is inside v1 or v2
    const inV1 = contentV1?.querySelector(`#${targetId}`);
    const inV2 = contentV2?.querySelector(`#${targetId}`);

    if (inV1 && currentVersion !== "v1" && !inV2) {
      setDocsVersion("v1", true);
    } else if (inV2 && currentVersion !== "v2" && !inV1) {
      setDocsVersion("v2", true);
    }

    const target = document.getElementById(targetId);
    if (target) {
      e.preventDefault();
      target.scrollIntoView({ behavior: "smooth", block: "start" });
      history.pushState(null, "", `#${targetId}`);
    }
  });

  // =========================================================================
  // MOBILE NAV DRAWER (shared header)
  // =========================================================================
  const mobileMenuToggle = document.getElementById("mobile-menu-toggle");
  const mobileNavDrawer = document.getElementById("mobile-nav-drawer");

  if (mobileMenuToggle && mobileNavDrawer) {
    mobileMenuToggle.addEventListener("click", () => {
      const isOpen = mobileNavDrawer.classList.toggle("open");
      mobileMenuToggle.classList.toggle("active", isOpen);
      mobileMenuToggle.setAttribute("aria-expanded", isOpen);
    });

    document.querySelectorAll(".mobile-nav-link").forEach((link) => {
      link.addEventListener("click", () => {
        mobileNavDrawer.classList.remove("open");
        mobileMenuToggle.classList.remove("active");
        mobileMenuToggle.setAttribute("aria-expanded", "false");
      });
    });
  }
});

/* =========================================================================
   TZRO DOCS — CLIENT-SIDE INTERACTIVITY
   Scroll-spy, sidebar, copy buttons, mobile drawer
   ========================================================================= */

document.addEventListener("DOMContentLoaded", () => {
  // =========================================================================
  // SCROLL-SPY — Right-hand TOC highlighting
  // =========================================================================
  const tocLinks = document.querySelectorAll(".docs-toc-link");
  const sectionHeadings = [];

  tocLinks.forEach((link) => {
    const targetId = link.getAttribute("href")?.replace("#", "");
    if (targetId) {
      const el = document.getElementById(targetId);
      if (el) sectionHeadings.push({ id: targetId, el, link });
    }
  });

  function updateTocHighlight() {
    const scrollY = window.scrollY + 120;

    let activeId = null;
    for (let i = sectionHeadings.length - 1; i >= 0; i--) {
      if (sectionHeadings[i].el.offsetTop <= scrollY) {
        activeId = sectionHeadings[i].id;
        break;
      }
    }

    tocLinks.forEach((link) => {
      const targetId = link.getAttribute("href")?.replace("#", "");
      link.classList.toggle("active", targetId === activeId);
    });
  }

  // =========================================================================
  // SCROLL-SPY — Left sidebar active state
  // =========================================================================
  const sidebarLinks = document.querySelectorAll(".docs-sidebar-link");
  const sidebarSections = [];

  sidebarLinks.forEach((link) => {
    const targetId = link.getAttribute("href")?.replace("#", "");
    if (targetId) {
      const el = document.getElementById(targetId);
      if (el) sidebarSections.push({ id: targetId, el, link });
    }
  });

  function updateSidebarHighlight() {
    const scrollY = window.scrollY + 120;

    let activeId = null;
    for (let i = sidebarSections.length - 1; i >= 0; i--) {
      if (sidebarSections[i].el.offsetTop <= scrollY) {
        activeId = sidebarSections[i].id;
        break;
      }
    }

    sidebarLinks.forEach((link) => {
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

  // Initialize on load
  updateTocHighlight();
  updateSidebarHighlight();

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

  // Close sidebar when a link is clicked (mobile)
  sidebarLinks.forEach((link) => {
    link.addEventListener("click", () => {
      if (window.innerWidth <= 1024) {
        closeSidebar();
      }
    });
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
  // SMOOTH SCROLL for anchor links
  // =========================================================================
  document.querySelectorAll('a[href^="#"]').forEach((anchor) => {
    anchor.addEventListener("click", (e) => {
      const targetId = anchor.getAttribute("href")?.replace("#", "");
      if (!targetId) return;
      const target = document.getElementById(targetId);
      if (target) {
        e.preventDefault();
        target.scrollIntoView({ behavior: "smooth", block: "start" });
        // Update URL without jumping
        history.pushState(null, "", `#${targetId}`);
      }
    });
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

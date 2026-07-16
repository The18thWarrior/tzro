/* =========================================================================
   TZRO WEBSITE V2: CORE CLIENT-SIDE ENGINE
   ========================================================================= */

document.addEventListener("DOMContentLoaded", () => {
  // =========================================================================
  // DUAL-AUDIENCE MODE TOGGLE SYSTEM
  // =========================================================================
  const modeToggleDesktop = document.getElementById("mode-toggle-desktop");
  const modeToggleMobile = document.getElementById("mode-toggle-mobile");

  function setMode(mode) {
    document.body.setAttribute("data-mode", mode);
    localStorage.setItem("tzro-site-mode", mode);

    // Update URL param
    const url = new URL(location);
    url.searchParams.set("view", mode);
    history.replaceState(null, "", url);

    // Sync all toggle button states
    document.querySelectorAll(".mode-toggle-btn").forEach((btn) => {
      btn.classList.toggle("active", btn.getAttribute("data-mode") === mode);
    });
  }

  function initMode() {
    // GTM 2026: Default to user mode. Developer toggle is hidden.
    // ?view=developer URL param still works as an internal backdoor.
    const urlMode = new URLSearchParams(location.search).get("view");
    const mode = urlMode === "developer" ? "developer" : "user";
    setMode(mode);
  }

  // Attach click handlers to both desktop and mobile toggles
  [modeToggleDesktop, modeToggleMobile].forEach((toggle) => {
    if (!toggle) return;
    toggle.querySelectorAll(".mode-toggle-btn").forEach((btn) => {
      btn.addEventListener("click", () => {
        setMode(btn.getAttribute("data-mode"));
      });
    });
  });

  initMode();

  // =========================================================================
  // Mobile Navigation Drawer Toggle
  // =========================================================================
  const mobileMenuToggle = document.getElementById("mobile-menu-toggle");
  const mobileNavDrawer = document.getElementById("mobile-nav-drawer");
  const mobileNavLinks = document.querySelectorAll(".mobile-nav-link");

  if (mobileMenuToggle && mobileNavDrawer) {
    mobileMenuToggle.addEventListener("click", () => {
      const isOpen = mobileNavDrawer.classList.toggle("open");
      mobileMenuToggle.classList.toggle("active", isOpen);
      mobileMenuToggle.setAttribute("aria-expanded", isOpen);
    });

    mobileNavLinks.forEach((link) => {
      link.addEventListener("click", () => {
        mobileNavDrawer.classList.remove("open");
        mobileMenuToggle.classList.remove("active");
        mobileMenuToggle.setAttribute("aria-expanded", "false");
      });
    });

    document.addEventListener("click", (e) => {
      if (
        mobileNavDrawer.classList.contains("open") &&
        !mobileNavDrawer.contains(e.target) &&
        !mobileMenuToggle.contains(e.target)
      ) {
        mobileNavDrawer.classList.remove("open");
        mobileMenuToggle.classList.remove("active");
        mobileMenuToggle.setAttribute("aria-expanded", "false");
      }
    });
  }

  // =========================================================================
  // GITHUB STAR COUNT FETCHER
  // =========================================================================
  const starCountEl = document.getElementById("github-star-count");
  if (starCountEl) {
    fetch("https://api.github.com/repos/The18thWarrior/tzro", {
      headers: { Accept: "application/vnd.github.v3+json" },
    })
      .then((res) => {
        if (!res.ok) throw new Error("GitHub API error");
        return res.json();
      })
      .then((data) => {
        const count = data.stargazers_count;
        if (typeof count === "number") {
          starCountEl.textContent = count >= 1000
            ? (count / 1000).toFixed(1) + "k"
            : count.toString();
        }
      })
      .catch(() => {
        // Silently fail — keep the "—" placeholder
      });
  }

  // =========================================================================
  // CLICK-TO-COPY INSTALLER COMMAND
  // =========================================================================
  const installString =
    "curl -fsSL https://get.tzro.ai | sh";

  function setupCopyButton(codeBlockId, buttonId) {
    const codeBlock = document.getElementById(codeBlockId);
    const copyBtn = document.getElementById(buttonId);
    if (!codeBlock || !copyBtn) return;

    async function executeCopy() {
      try {
        await navigator.clipboard.writeText(installString);
        copyBtn.textContent = "Copied!";
        copyBtn.classList.add("copied");
        setTimeout(() => {
          copyBtn.textContent = "Copy";
          copyBtn.classList.remove("copied");
        }, 2000);
      } catch (err) {
        console.error("Failed to copy installation string", err);
      }
    }

    codeBlock.addEventListener("click", executeCopy);
    copyBtn.addEventListener("click", (e) => {
      e.stopPropagation();
      executeCopy();
    });
  }

  // Set up both installer terminals (hero + install section)
  setupCopyButton("installer-copy-target", "installer-copy-btn");
  setupCopyButton("install-copy-target", "install-copy-btn");

  // =========================================================================
  // ARCHITECTURE PHASE CARD INTERACTIONS
  // =========================================================================
  const phaseCards = document.querySelectorAll(".phase-card");
  const phaseStdout = document.getElementById("phase-stdout-output");

  const PHASE_STDOUT_MESSAGES = [
    "❯ Initializing agent tool binding over stdio...",
    "❯ Daemon compiling task graph locally. Local workspace search complete.",
    "❯ Token footprint payload compressed. Sending optimized output to model context window.",
  ];

  if (phaseCards.length && phaseStdout) {
    phaseCards.forEach((card) => {
      card.addEventListener("click", () => {
        const phaseIndex = parseInt(card.getAttribute("data-phase"), 10);

        // Update active state
        phaseCards.forEach((c) => c.classList.remove("active"));
        card.classList.add("active");

        // Update stdout text
        if (PHASE_STDOUT_MESSAGES[phaseIndex]) {
          phaseStdout.textContent = PHASE_STDOUT_MESSAGES[phaseIndex];
        }
      });
    });
  }

  // =========================================================================
  // OS ARCHITECTURE DIAGRAM: INTERACTIVITY (Developer mode)
  // =========================================================================
  const osBlocks = document.querySelectorAll(".os-block");
  const osTooltip = document.getElementById("os-tooltip");
  const osTooltipText = document.getElementById("os-tooltip-text");

  osBlocks.forEach((block) => {
    // Click → smooth scroll to target section
    block.addEventListener("click", () => {
      const target = block.getAttribute("data-target");
      if (target) {
        const el = document.querySelector(target);
        if (el) {
          el.scrollIntoView({ behavior: "smooth", block: "start" });
        }
      }
    });

    // Hover → tooltip display
    block.addEventListener("mouseenter", (e) => {
      const label = block.getAttribute("data-label");
      if (label && osTooltip && osTooltipText) {
        osTooltipText.textContent = label;
        osTooltip.classList.add("visible");
        const rect = block.getBoundingClientRect();
        osTooltip.style.left = rect.left + rect.width / 2 + "px";
        osTooltip.style.top = rect.top - 10 + "px";
        osTooltip.style.transform = "translate(-50%, -100%)";
      }
    });

    block.addEventListener("mouseleave", () => {
      if (osTooltip) {
        osTooltip.classList.remove("visible");
      }
    });
  });

  // =========================================================================
  // SMOOTH SCROLL FOR NAV LINKS
  // =========================================================================
  document.querySelectorAll('a[href^="#"]').forEach((anchor) => {
    anchor.addEventListener("click", function (e) {
      const targetId = this.getAttribute("href");
      if (targetId && targetId !== "#") {
        const target = document.querySelector(targetId);
        if (target) {
          e.preventDefault();
          target.scrollIntoView({ behavior: "smooth", block: "start" });
        }
      }
    });
  });
});

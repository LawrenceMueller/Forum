// snowball.js: small bits of behavior for the whole site.
// Loaded with "defer", so it runs after the page's HTML has been read.

// ---- hidden usernames ----
// Any element with class "reveal-group" holds a masked name (.name-masked),
// the real one (.name-real, starts hidden), and a "show name" checkbox inside
// a label (.reveal, also starts hidden). The header has one; the account page
// has another. Each group works on its own.
document.querySelectorAll(".reveal-group").forEach(function (group) {
  const label = group.querySelector(".reveal");
  const toggle = label && label.querySelector("input[type=checkbox]");
  const realName = group.querySelector(".name-real");
  const masked = group.querySelector(".name-masked");
  if (!toggle || !realName || !masked) return;

  function update() {
    realName.hidden = !toggle.checked;
    masked.hidden = toggle.checked;
  }

  toggle.addEventListener("change", update);

  // The checkbox only appears when JavaScript is running, since without it
  // the box couldn't do anything.
  label.hidden = false;

  // Always start hidden. "pageshow" also fires when the back button restores
  // a page from the browser's memory, which would otherwise bring back a
  // revealed name exactly as it was left.
  window.addEventListener("pageshow", function () {
    toggle.checked = false;
    update();
  });
  update();
});

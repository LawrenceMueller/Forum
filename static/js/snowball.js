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

// ---- topic suggestions ----
// The suggestions themselves are plain htmx (see partials/tagpicker.html);
// this only smooths the keyboard side.

// After a suggestion is clicked, the server sends back the topics box with
// "autofocus" on it. Put the cursor at the end, ready for the next topic,
// rather than wherever the browser chooses.
document.body.addEventListener("htmx:afterSettle", function () {
  const input = document.querySelector("#topics[autofocus]");
  if (!input) return;
  input.removeAttribute("autofocus");
  input.focus();
  const end = input.value.length;
  input.setSelectionRange(end, end);
});

// Down arrow from the box goes to the first suggestion; up and down move
// between them; Escape goes back to the box. (Tab works too, as always.)
document.addEventListener("keydown", function (e) {
  const input = document.getElementById("topics");
  if (!input) return;
  const buttons = Array.from(document.querySelectorAll("#topic-help .suggestion"));
  const at = buttons.indexOf(document.activeElement);

  if (e.target === input && e.key === "ArrowDown" && buttons.length > 0) {
    e.preventDefault();
    buttons[0].focus();
  } else if (at >= 0 && e.key === "ArrowDown") {
    e.preventDefault();
    buttons[Math.min(at + 1, buttons.length - 1)].focus();
  } else if (at >= 0 && e.key === "ArrowUp") {
    e.preventDefault();
    if (at === 0) input.focus();
    else buttons[at - 1].focus();
  } else if (at >= 0 && e.key === "Escape") {
    e.preventDefault();
    input.focus();
  }
});

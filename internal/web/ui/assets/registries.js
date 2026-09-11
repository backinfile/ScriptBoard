document.addEventListener("change", (event) => {
  if (!event.target.matches("[data-registry-select-all]")) return;
  event.target.closest("form")?.querySelectorAll('input[name="repositories"]').forEach(input => { input.checked = event.target.checked; });
});

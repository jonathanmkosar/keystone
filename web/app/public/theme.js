// Sets the chosen theme before the page paints, so a person who chose light
// on a dark system never sees a dark flash. It is a file of its own because
// the server's content security policy allows no inline script.
try {
  var t = localStorage.getItem("keystone.theme");
  if (t === "dark" || t === "light") document.documentElement.setAttribute("data-theme", t);
} catch (e) {}

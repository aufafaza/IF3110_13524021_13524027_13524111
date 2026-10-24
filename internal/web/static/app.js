document.body.addEventListener("htmx:responseError", function (event) {
  var flash = document.getElementById("flash");
  if (!flash) {
    return;
  }
  var status = event.detail.xhr.status;
  if (status === 403) {
    flash.textContent = "Your session or security token is no longer valid. Reload the page and try again.";
  } else if (status === 404) {
    flash.textContent = "That item no longer exists. Reload the page.";
  } else {
    flash.textContent = "Something went wrong (" + status + "). Reload the page and try again.";
  }
});

document.body.addEventListener("htmx:sendError", function () {
  var flash = document.getElementById("flash");
  if (flash) {
    flash.textContent = "Could not reach the server.";
  }
});

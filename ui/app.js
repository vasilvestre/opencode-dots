// Entry point. It asks who is looking before it shows anything: with GitHub sign-in off
// (/api/session answers 404) the page is the owner's, as it always was; an anonymous visitor
// gets the sign-in page and nothing else; the owner and a signed-in visitor get the dashboard.
import { loadSession, showSignIn } from "./session.js";

const session = await loadSession();
document.documentElement.dataset.role = session.role;
if (session.role === "anonymous") showSignIn(document.querySelector("#view"));
else await import("./boot.js");

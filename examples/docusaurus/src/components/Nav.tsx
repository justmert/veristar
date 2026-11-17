import Link from "@docusaurus/Link";

// links inside the site go through docusaurus's router, which changes the
// url with history.pushState
export default function Nav() {
  return (
    <nav>
      <Link to="/" id="nav-home">
        Home
      </Link>{" "}
      <Link to="/pricing" id="nav-pricing">
        Pricing
      </Link>{" "}
      <Link to="/docs/getting-started" id="nav-docs">
        Docs
      </Link>
    </nav>
  );
}

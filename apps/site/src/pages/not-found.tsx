import { Footer } from "../components/footer";
import { Header } from "../components/header";
import { action, Reveal } from "../components/ui";
import { site } from "../links";

// served by the host for any path the site does not have
export function NotFoundPage() {
  const docs = site.docs();
  return (
    <>
      <Header onHome={false} />
      <main className="border-b border-line">
        <Reveal className="mx-auto flex max-w-content flex-col items-start gap-4 px-4 py-20 sm:px-6 sm:py-32">
          <span className="font-mono text-small text-fg-3">404</span>
          <h1 className="text-h1 tracking-tight">Page not found</h1>
          <p className="max-w-prose text-title font-normal text-fg-2">
            This address does not exist on the Veristar website. Project dashboards live in the
            console, and guides in the documentation.
          </p>
          <div className="mt-2 flex w-full flex-col gap-2.5 sm:w-auto sm:flex-row">
            <a href="/" className={action.primary}>
              Go to the home page
            </a>
            {site.signIn && (
              <a href={site.signIn} className={action.outline}>
                Sign in to the console
              </a>
            )}
            {docs && (
              <a href={docs} className={action.outline}>
                Read the docs
              </a>
            )}
          </div>
        </Reveal>
      </main>
      <Footer />
    </>
  );
}

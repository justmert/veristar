import { Mark } from "@veristar/brand/mark";
import { site } from "../links";

type Group = { title: string; links: { label: string; href: string | null }[] };

// a link whose host is unknown on this address is left out rather than
// pointed at a guess. the source link is added once the repository is
// public (README.md status: the public repository is still pending).
function groups(): Group[] {
  return [
    {
      title: "Product",
      links: [
        { label: "Console", href: site.signIn },
        { label: "Docs", href: site.docs() },
        { label: "Status", href: site.status },
      ],
    },
    {
      title: "Project",
      links: [
        { label: "Support", href: site.docs("support") },
        { label: "Security", href: site.docs("security/SECURITY") },
        { label: "Handover plan", href: site.docs("handover/plan") },
      ],
    },
    {
      title: "Legal",
      links: [
        { label: "Terms of use", href: site.docs("compliance/terms") },
        { label: "Privacy strategy", href: site.docs("compliance/privacy-strategy") },
        { label: "Data residency", href: site.docs("compliance/data-residency") },
      ],
    },
  ]
    .map((g) => ({ ...g, links: g.links.filter((l) => l.href) }))
    .filter((g) => g.links.length > 0);
}

export function Footer() {
  return (
    <footer>
      <div className="mx-auto flex max-w-content flex-wrap gap-x-16 gap-y-8 px-4 pt-8 pb-10 sm:px-6 sm:pt-10 sm:pb-12">
        <div className="flex min-w-60 flex-1 flex-col gap-2.5">
          <span className="flex items-center gap-2">
            <Mark className="size-5" />
            <span className="text-body font-semibold tracking-tight">Veristar</span>
          </span>
          <span className="text-small text-fg-2">Free for every project. No fees for anyone.</span>
        </div>
        <div className="grid grid-cols-2 gap-5 sm:flex sm:gap-16">
          {groups().map((g) => (
            <nav key={g.title} aria-label={g.title} className="flex flex-col gap-1 text-body">
              <span className="text-caption text-fg-3">{g.title}</span>
              {g.links.map((l) => (
                <a
                  key={l.label}
                  href={l.href ?? undefined}
                  className="flex min-h-(--vs-control-touch) items-center rounded-sm sm:min-h-9 text-fg transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg-2 focus-ring"
                >
                  {l.label}
                </a>
              ))}
            </nav>
          ))}
        </div>
      </div>
    </footer>
  );
}

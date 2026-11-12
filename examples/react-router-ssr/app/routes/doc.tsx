import type { Route } from "./+types/doc";

export function loader({ params }: Route.LoaderArgs) {
  return { slug: params.slug };
}

export function meta({ params }: Route.MetaArgs) {
  return [{ title: `React Router example: ${params.slug}` }];
}

export default function Doc({ loaderData }: Route.ComponentProps) {
  return <h1>Docs: {loaderData.slug}</h1>;
}

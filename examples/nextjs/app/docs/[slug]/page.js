export default async function Doc({ params }) {
  const { slug } = await params;
  return (
    <>
      <title>{`Next.js example: ${slug}`}</title>
      <h1>Docs: {slug}</h1>
    </>
  );
}

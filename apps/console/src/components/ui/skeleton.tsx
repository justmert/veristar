import { cn } from "cn";

// a placeholder in the shape of what is coming; it breathes, it never spins
function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="skeleton"
      aria-hidden
      className={cn("animate-skeleton rounded-sm bg-muted", className)}
      {...props}
    />
  );
}

export { Skeleton };

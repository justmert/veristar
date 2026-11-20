import { Link } from "react-router";
import { PublicFrame } from "@/components/public-frame";
import { MessagePage } from "@/components/states";
import { Button } from "@/components/ui/button";

// the address an older version of the console sent people to after a
// single sign-on. the sign-on is gone; the page says so and leads to the
// sign-in, after which the projects and every dashboard show.
export function SsoErrorPage() {
  return (
    <PublicFrame>
      <MessagePage
        code="Old link"
        title="This link no longer works"
        actions={
          <Button asChild className="max-sm:h-(--vs-control-touch)">
            <Link to="/sign-in">Sign in</Link>
          </Button>
        }
      >
        <p>It is from an older version of the console. Your projects are in the console.</p>
      </MessagePage>
    </PublicFrame>
  );
}

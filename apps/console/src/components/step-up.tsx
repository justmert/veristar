import { KeyRound } from "lucide-react";
import { type FormEvent, useState } from "react";
import { Link } from "react-router";
import { operatorKeys, operatorsApi, type StepUpStatus } from "@/api/operators";
import { keys } from "@/api/queries";
import { TextField } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { Panel } from "@/components/panel";
import { PasskeyDialog } from "@/components/passkey-dialog";
import { Button } from "@/components/ui/button";
import { formatDateTimeUtc } from "@/lib/format";
import { passkeysAvailable } from "@/lib/passkey";
import { queryClient } from "@/query-client";

// after a step-up or a new passkey the operator area loads again with the
// new session
export async function steppedUp(s?: StepUpStatus) {
  if (s) queryClient.setQueryData(operatorKeys.stepUp, s);
  await queryClient.invalidateQueries({ queryKey: operatorKeys.stepUp });
  await queryClient.invalidateQueries({ queryKey: keys.operator });
}

function lasting(seconds: number): string {
  if (seconds < 3600) return `${Math.round(seconds / 60)} minutes`;
  const h = Math.round(seconds / 3600);
  return h === 1 ? "1 hour" : `${h} hours`;
}

// AddPasskeyDialog registers a passkey for the step-up (contract 5.15) with
// the console's one passkey dialog: create the passkey, then confirm it once
// over a challenge of the step-up, which proves the key and records the
// step-up. name is what the operators page lists it as.
export function AddPasskeyDialog({
  open,
  onOpenChange,
  name,
  userName,
  onAdded,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  name: string;
  userName: string;
  onAdded?: () => void;
}) {
  return (
    <PasskeyDialog
      open={open}
      onOpenChange={onOpenChange}
      mode="link"
      title="Add a passkey"
      description="Create a passkey on this device or a security key, then confirm it once."
      userName={userName}
      challenge={() => operatorsApi.passkeyChallenge()}
      verify={async (answer) => {
        await operatorsApi.addPasskey(name, answer);
        await steppedUp();
        onOpenChange(false);
        onAdded?.();
      }}
    />
  );
}

// the operator area asks for a passkey before it shows anything (contract
// 5.15). the control plane enforces it on every route; this panel is how the
// person answers. userName is what the browser's passkey list shows for a
// passkey created here.
export function StepUpGate({ status, userName }: { status: StepUpStatus; userName: string }) {
  if (!passkeysAvailable())
    return (
      <Notice tone="critical" title="This browser cannot use passkeys.">
        The operator area needs one. Open the console in a current version of Chrome, Edge, Firefox
        or Safari.
      </Notice>
    );
  return (
    <div className="flex w-full max-w-prose animate-rise-in flex-col gap-4">
      {(status.passkeys ?? 0) > 0 ? (
        <ConfirmPanel maxAgeSeconds={status.max_age_seconds} />
      ) : status.enrollment === "open" ? (
        <EnrollPanel until={status.enrollment_until ?? null} userName={userName} />
      ) : status.enrollment === "sign_in_required" ? (
        <Notice
          tone="warning"
          title="Sign in again to register your first passkey."
          actions={
            <Button asChild variant="outline" size="sm">
              <Link to="/sign-in?return_to=%2Foperator">Sign in again</Link>
            </Button>
          }
        >
          Your first passkey can be registered from a session that started after your operator
          access was set up.
        </Notice>
      ) : (
        <Notice tone="warning" title="Your account has no passkey for the operator area.">
          Ask an operator admin to reset your passkeys on the Operators page. The reset gives you a
          new window to register one.
        </Notice>
      )}
    </div>
  );
}

function ConfirmPanel({ maxAgeSeconds }: { maxAgeSeconds: number }) {
  const [open, setOpen] = useState(false);
  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          <KeyRound aria-hidden className="size-4 text-fg-2" />
          Confirm it is you
        </span>
      }
      description={`The operator area needs your passkey. The confirmation lasts ${lasting(maxAgeSeconds)} in this browser session.`}
    >
      <div>
        <Button onClick={() => setOpen(true)}>Use your passkey</Button>
      </div>
      <PasskeyDialog
        open={open}
        onOpenChange={setOpen}
        mode="confirm"
        title="Confirm with your passkey"
        userName=""
        challenge={() => operatorsApi.stepUpChallenge()}
        verify={async (answer) => {
          const s = await operatorsApi.stepUp(answer);
          await steppedUp(s);
          setOpen(false);
        }}
      />
    </Panel>
  );
}

function EnrollPanel({ until, userName }: { until: string | null; userName: string }) {
  const [name, setName] = useState("Passkey");
  const [nameError, setNameError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  function submit(e: FormEvent) {
    e.preventDefault();
    const n = name.trim();
    if (n === "" || n.length > 64) {
      setNameError("Give the passkey a name of 1 to 64 characters.");
      return;
    }
    setNameError(null);
    setOpen(true);
  }
  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          <KeyRound aria-hidden className="size-4 text-fg-2" />
          Register a passkey
        </span>
      }
      description={
        <>
          The operator area needs a passkey on top of your sign-in. Register your first one now
          {until ? `, before ${formatDateTimeUtc(until)}` : ""}. Later ones need a confirmation with
          this one.
        </>
      }
    >
      <form onSubmit={submit} className="flex flex-col gap-3" noValidate>
        <TextField
          label="Name"
          name="passkey-name"
          hint="So you can tell your passkeys apart, for example the device or the security key's label."
          value={name}
          maxLength={64}
          onChange={(e) => setName(e.target.value)}
          error={nameError}
        />
        <div>
          <Button type="submit">Register</Button>
        </div>
      </form>
      <AddPasskeyDialog open={open} onOpenChange={setOpen} name={name.trim()} userName={userName} />
    </Panel>
  );
}

import { useMutation } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { type FormEvent, useState } from "react";
import { Link } from "react-router";
import { operatorKeys, operatorsApi, type StepUpStatus } from "@/api/operators";
import { keys } from "@/api/queries";
import { FormMessage } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { Panel } from "@/components/panel";
import { Button } from "@/components/ui/button";
import { formatDateTimeUtc } from "@/lib/format";
import { createPasskey, getAssertion, webauthnAvailable } from "@/lib/webauthn";
import { queryClient } from "@/query-client";

// after a step-up or a first passkey the operator area loads again with
// the new session
async function steppedUp(s?: StepUpStatus) {
  if (s) queryClient.setQueryData(operatorKeys.stepUp, s);
  await queryClient.invalidateQueries({ queryKey: operatorKeys.stepUp });
  await queryClient.invalidateQueries({ queryKey: keys.operator });
}

export async function registerPasskey(name: string) {
  const options = await operatorsApi.passkeyOptions();
  const credential = await createPasskey(options);
  return operatorsApi.addPasskey(name, credential);
}

// the operator area asks for a passkey or security key before it shows
// anything (contract 5.15). the control plane enforces it on every route;
// this panel is how the person answers.
export function StepUpGate({ status }: { status: StepUpStatus }) {
  if (!webauthnAvailable())
    return (
      <Notice tone="critical" title="This browser cannot use passkeys or security keys.">
        The operator area needs one. Open the console in a current browser over HTTPS.
      </Notice>
    );
  return (
    <div className="mx-auto flex w-full max-w-prose animate-rise-in flex-col gap-4">
      {(status.passkeys ?? 0) > 0 ? (
        <ConfirmPanel maxAgeSeconds={status.max_age_seconds} />
      ) : status.enrollment === "open" ? (
        <EnrollPanel until={status.enrollment_until ?? null} />
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

function lasting(seconds: number): string {
  if (seconds < 3600) return `${Math.round(seconds / 60)} minutes`;
  const h = Math.round(seconds / 3600);
  return h === 1 ? "1 hour" : `${h} hours`;
}

function ConfirmPanel({ maxAgeSeconds }: { maxAgeSeconds: number }) {
  const m = useMutation({
    mutationFn: async () => {
      const options = await operatorsApi.stepUpOptions();
      const credential = await getAssertion(options);
      return operatorsApi.stepUp(credential);
    },
    onSuccess: steppedUp,
  });
  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          <KeyRound aria-hidden className="size-4 text-fg-2" />
          Confirm it is you
        </span>
      }
      description={`The operator area needs your passkey or security key. The confirmation lasts ${lasting(maxAgeSeconds)} in this browser session.`}
    >
      <div className="flex flex-col gap-3">
        <div>
          <Button onClick={() => m.mutate()} pending={m.isPending}>
            {m.isPending ? "Waiting for your passkey" : "Use passkey or security key"}
          </Button>
        </div>
        <FormMessage error={m.error} />
      </div>
    </Panel>
  );
}

function EnrollPanel({ until }: { until: string | null }) {
  const [name, setName] = useState("Security key");
  const [nameError, setNameError] = useState<string | null>(null);
  const m = useMutation({
    mutationFn: (n: string) => registerPasskey(n),
    onSuccess: () => steppedUp(),
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    const n = name.trim();
    if (n === "" || n.length > 64) {
      setNameError("Give the passkey a name of 1 to 64 characters.");
      return;
    }
    setNameError(null);
    m.mutate(n);
  }
  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          <KeyRound aria-hidden className="size-4 text-fg-2" />
          Register a passkey or security key
        </span>
      }
      description={
        <>
          The operator area needs a second factor on top of your sign-in. Register your first one
          now{until ? `, before ${formatDateTimeUtc(until)}` : ""}. Later ones need a confirmation
          with this one.
        </>
      }
    >
      <form onSubmit={submit} className="flex flex-col gap-3" noValidate>
        <TextField
          label="Name"
          name="passkey-name"
          hint="So you can tell your keys apart, for example the device or the key's label."
          value={name}
          maxLength={64}
          onChange={(e) => setName(e.target.value)}
          error={nameError}
        />
        <div>
          <Button type="submit" pending={m.isPending}>
            {m.isPending ? "Waiting for your passkey" : "Register"}
          </Button>
        </div>
        <FormMessage error={m.error} />
      </form>
    </Panel>
  );
}

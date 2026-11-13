// window.umami as sdk.js installs it; optional because a visitor may block it
interface Window {
  umami?: {
    track: (name: string, data?: Record<string, string | number | boolean>) => Promise<void>;
  };
}

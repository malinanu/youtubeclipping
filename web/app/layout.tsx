import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "ClipForge",
  description: "Clip YouTube & Twitch videos with styled captions",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="" />
        {/* Same families the renderer uses (installed in the worker image) so caption previews match. */}
        <link
          rel="stylesheet"
          href="https://fonts.googleapis.com/css2?family=Bebas+Neue&family=Inter:wght@400;500;600;700;800;900&family=Montserrat:wght@500;600;700;800;900&family=League+Spartan:wght@600;700;800;900&family=Lato:wght@700;900&family=Roboto:wght@500;700;900&display=swap"
        />
      </head>
      <body>{children}</body>
    </html>
  );
}

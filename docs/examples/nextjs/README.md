# Next.js Example

Sandbox configuration for Next.js applications with API routes.

## Setup

```bash
cd your-nextjs-project
cp /path/to/watermelon/docs/examples/nextjs/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
npm install
npm run dev
# Visit http://localhost:3000 on your host
```

## API Routes and External Services

If your API routes call external services, add them to the allowlist:

```toml
[network]
allow = [
    "registry.npmjs.org",
    "api.openai.com",        # OpenAI API
    "*.supabase.co",         # Supabase
    "api.stripe.com",        # Stripe
]
```

## Environment Variables

Your `.env.local` file is automatically available inside the sandbox since it's part of your project. But your host's environment variables are NOT inherited, keeping your system credentials safe.

# Contact form

The existing Go Cloud Run service handles POST /api/contact and sends plain-text
email through Resend. Static files remain in web/. No database is used.

Runtime configuration (never put the API key in source control):

- RESEND_API_KEY: Secret Manager reference, pinned to a secret version.
- CONTACT_FROM: contact@notifications.sayanc.me (verified Resend sending domain).
- CONTACT_TO: sayanc432@gmail.com.
- GOOGLE_CLOUD_PROJECT: sayan-portfolio-353810.
- RECAPTCHA_SITE_KEY: public Enterprise checkbox key restricted to the site domain.
- CONTACT_ORIGIN: exact HTTPS origin; development is https://dev.sayanc.me.

The development runtime identity is
sayancdev-contact@sayan-portfolio-353810.iam.gserviceaccount.com. It needs
roles/recaptchaenterprise.agent on the project and roles/secretmanager.secretAccessor
on sayancdev-resend-api-key only. No service-account key file is needed.

Google's metadata server supplies the runtime identity token. The browser receives
only the public CAPTCHA site key. Names, addresses and message bodies are not logged
or stored in a database. Resend and the recipient's mailbox process the email.

Protection includes origin checks, bounded request bodies, input validation, a
honeypot, server-side CAPTCHA verification and mutex-protected in-memory limits.
Each instance permits at most 20 CAPTCHA attempts/minute, 3 email attempts/minute,
50 email attempts/UTC day and one attempt per hashed sender address per 10 minutes.
These are per-instance limits: restarts reset them and scaling multiplies them.
They are not a global spending cap. Resend idempotency keys reduce duplicate sends
after uncertain responses; failures preserve text in the open browser form.

Run go test ./... and go vet ./... before release. Without runtime configuration,
the form shows an unavailable message and a direct email link. Mock tests do not
send real email. Development end-to-end testing requires a human CAPTCHA and
confirmation that the test email arrived. Production promotion requires the owner's
explicit approval, a production CAPTCHA key/origin and production secret configuration.

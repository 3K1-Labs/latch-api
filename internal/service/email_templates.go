package service

// otpTemplate is the HTML body for email verification OTPs.
// %s is replaced with the 6-digit OTP code via fmt.Sprintf in email.go.
const otpTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Verify your email – Latch</title>
</head>
<body style="margin:0;padding:0;background-color:#EEEEF5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" role="presentation">
    <tr>
      <td align="center" style="padding:48px 16px;">

        <!-- Card -->
        <table width="100%%" cellpadding="0" cellspacing="0" role="presentation"
               style="max-width:480px;border-radius:20px;overflow:hidden;box-shadow:0 8px 40px rgba(0,0,0,0.10);">

          <!-- Header -->
          <tr>
            <td bgcolor="#0B0B14" style="padding:32px 40px 28px;">
              <!-- Logo: icon + wordmark -->
              <table cellpadding="0" cellspacing="0" role="presentation">
                <tr>
                  <!-- Logo image -->
                  <td style="vertical-align:middle;">
                    <img src="https://res.cloudinary.com/dqm97vrty/image/upload/v1778953244/logoLoading_gcurdp.png"
                         width="48" height="40" alt="Latch" style="display:block;border:0;">
                  </td>
                  <!-- Wordmark -->
                  <td style="padding-left:14px;vertical-align:middle;">
                    <span style="color:#FFFFFF;font-size:22px;font-weight:800;letter-spacing:4px;display:block;line-height:1;">LATCH</span>
                    <span style="color:#6666AA;font-size:11px;letter-spacing:1.5px;display:block;margin-top:3px;">STELLAR WALLET</span>
                  </td>
                </tr>
              </table>
            </td>
          </tr>

          <!-- Purple accent bar -->
          <tr>
            <td bgcolor="#7C5CFC" style="height:3px;font-size:0;line-height:0;">&nbsp;</td>
          </tr>

          <!-- Body -->
          <tr>
            <td bgcolor="#FFFFFF" style="padding:40px 40px 32px;">
              <h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#0B0B14;line-height:1.3;">
                Verify your email
              </h1>
              <p style="margin:0 0 32px;font-size:15px;color:#64648A;line-height:1.7;">
                Use the code below to complete your sign-in to Latch.<br>
                It expires in <strong style="color:#0B0B14;">10 minutes</strong>.
              </p>

              <!-- OTP block -->
              <table width="100%%" cellpadding="0" cellspacing="0" role="presentation">
                <tr>
                  <td align="center" bgcolor="#F5F5FA"
                      style="background:#F5F5FA;border-radius:14px;padding:28px 16px;border:1.5px solid #E0E0EE;">
                    <span style="font-size:48px;font-weight:800;letter-spacing:14px;color:#0B0B14;
                                 font-variant-numeric:tabular-nums;display:block;line-height:1;">
                      %s
                    </span>
                    <span style="font-size:12px;color:#9999BB;letter-spacing:0.5px;display:block;margin-top:10px;">
                      VERIFICATION CODE
                    </span>
                  </td>
                </tr>
              </table>

              <p style="margin:28px 0 0;font-size:13px;color:#AAAACC;line-height:1.7;">
                If you didn't request this, you can safely ignore this email —
                your account has not been accessed.
              </p>
            </td>
          </tr>

          <!-- Footer -->
          <tr>
            <td bgcolor="#FFFFFF" style="padding:0 40px 36px;">
              <table width="100%%" cellpadding="0" cellspacing="0" role="presentation">
                <tr>
                  <td style="border-top:1px solid #EBEBF5;padding-top:24px;">
                    <p style="margin:0;font-size:12px;color:#BBBBCC;text-align:center;line-height:1.8;">
                      © 2026 Latch &nbsp;·&nbsp; Built on Stellar<br>
                      <a href="#" style="color:#7C5CFC;text-decoration:none;">Privacy Policy</a>
                      &nbsp;&nbsp;
                      <a href="#" style="color:#7C5CFC;text-decoration:none;">Terms of Service</a>
                    </p>
                  </td>
                </tr>
              </table>
            </td>
          </tr>

        </table>
      </td>
    </tr>
  </table>
</body>
</html>`

// recoveryTemplate is the HTML body for account recovery OTPs.
// %s is replaced with the 6-digit OTP code via fmt.Sprintf in email.go.
const recoveryTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Account recovery – Latch</title>
</head>
<body style="margin:0;padding:0;background-color:#EEEEF5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" role="presentation">
    <tr>
      <td align="center" style="padding:48px 16px;">

        <!-- Card -->
        <table width="100%%" cellpadding="0" cellspacing="0" role="presentation"
               style="max-width:480px;border-radius:20px;overflow:hidden;box-shadow:0 8px 40px rgba(0,0,0,0.10);">

          <!-- Header -->
          <tr>
            <td bgcolor="#0B0B14" style="padding:32px 40px 28px;">
              <table cellpadding="0" cellspacing="0" role="presentation">
                <tr>
                  <td style="vertical-align:middle;">
                    <img src="https://res.cloudinary.com/dqm97vrty/image/upload/v1778953244/logoLoading_gcurdp.png"
                         width="48" height="40" alt="Latch" style="display:block;border:0;">
                  </td>
                  <td style="padding-left:14px;vertical-align:middle;">
                    <span style="color:#FFFFFF;font-size:22px;font-weight:800;letter-spacing:4px;display:block;line-height:1;">LATCH</span>
                    <span style="color:#6666AA;font-size:11px;letter-spacing:1.5px;display:block;margin-top:3px;">STELLAR WALLET</span>
                  </td>
                </tr>
              </table>
            </td>
          </tr>

          <!-- Warning accent bar -->
          <tr>
            <td bgcolor="#F5A623" style="height:3px;font-size:0;line-height:0;">&nbsp;</td>
          </tr>

          <!-- Warning banner -->
          <tr>
            <td bgcolor="#FFFBF2" style="padding:16px 40px;border-bottom:1px solid #FDE8BB;">
              <p style="margin:0;font-size:13px;color:#8B6200;line-height:1.5;">
                &#9888;&#65039;&nbsp;&nbsp;<strong>Security alert:</strong> A recovery was requested for your account.
                If this wasn't you, secure your email immediately.
              </p>
            </td>
          </tr>

          <!-- Body -->
          <tr>
            <td bgcolor="#FFFFFF" style="padding:40px 40px 32px;">
              <h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#0B0B14;line-height:1.3;">
                Account recovery
              </h1>
              <p style="margin:0 0 32px;font-size:15px;color:#64648A;line-height:1.7;">
                Use the code below to recover access to your Latch wallet.<br>
                It expires in <strong style="color:#0B0B14;">10 minutes</strong> and can only be used once.
              </p>

              <!-- OTP block -->
              <table width="100%%" cellpadding="0" cellspacing="0" role="presentation">
                <tr>
                  <td align="center" bgcolor="#FFFBF2"
                      style="background:#FFFBF2;border-radius:14px;padding:28px 16px;border:1.5px solid #FDE8BB;">
                    <span style="font-size:48px;font-weight:800;letter-spacing:14px;color:#0B0B14;
                                 font-variant-numeric:tabular-nums;display:block;line-height:1;">
                      %s
                    </span>
                    <span style="font-size:12px;color:#B08030;letter-spacing:0.5px;display:block;margin-top:10px;">
                      RECOVERY CODE
                    </span>
                  </td>
                </tr>
              </table>

              <!-- What happens next -->
              <table width="100%%" cellpadding="0" cellspacing="0" role="presentation" style="margin-top:28px;">
                <tr>
                  <td bgcolor="#F5F5FA" style="background:#F5F5FA;border-radius:10px;padding:20px 24px;">
                    <p style="margin:0 0 8px;font-size:13px;font-weight:700;color:#0B0B14;">What happens next</p>
                    <p style="margin:0;font-size:13px;color:#64648A;line-height:1.7;">
                      After entering this code you'll be able to access your encrypted wallet backup.
                      Your private keys never leave your device.
                    </p>
                  </td>
                </tr>
              </table>

              <p style="margin:28px 0 0;font-size:13px;color:#AAAACC;line-height:1.7;">
                Didn't request this? Ignore this email — your wallet is safe.
                Consider changing your email password as a precaution.
              </p>
            </td>
          </tr>

          <!-- Footer -->
          <tr>
            <td bgcolor="#FFFFFF" style="padding:0 40px 36px;">
              <table width="100%%" cellpadding="0" cellspacing="0" role="presentation">
                <tr>
                  <td style="border-top:1px solid #EBEBF5;padding-top:24px;">
                    <p style="margin:0;font-size:12px;color:#BBBBCC;text-align:center;line-height:1.8;">
                      © 2026 Latch &nbsp;·&nbsp; Built on Stellar<br>
                      <a href="#" style="color:#7C5CFC;text-decoration:none;">Privacy Policy</a>
                      &nbsp;&nbsp;
                      <a href="#" style="color:#7C5CFC;text-decoration:none;">Terms of Service</a>
                    </p>
                  </td>
                </tr>
              </table>
            </td>
          </tr>

        </table>
      </td>
    </tr>
  </table>
</body>
</html>`

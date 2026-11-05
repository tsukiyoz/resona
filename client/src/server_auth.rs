//! Shared-password PAKE precedes Hello and account restoration. The discovered
//! Noise key is untrusted until this exchange authenticates its channel binding.
use crate::{
    noise::{CancellationToken, Connection},
    session::{Error, Result},
};
use opaque_ke::{
    CipherSuite, ClientLogin, ClientLoginFinishParameters, CredentialResponse, Identifiers,
    Ristretto255, TripleDh, ksf::Identity, rand::rngs::OsRng,
};
use tokio::time::Instant;
use zeroize::Zeroize;

struct Suite;
impl CipherSuite for Suite {
    type OprfCs = Ristretto255;
    type KeyExchange = TripleDh<Ristretto255, sha2::Sha512>;
    // Server passwords are configuration secrets, not user password records.
    // Match the server's RFC 9807 Identity KSF; account hashing is separate.
    type Ksf = Identity;
}

/// Complete server authentication on a provisional Noise transport. Call before
/// sending any identity or account data; close the connection on every error.
pub async fn authenticate(
    conn: &mut Connection,
    password: &str,
    pinned: bool,
    deadline: Instant,
    cancel: &CancellationToken,
) -> Result<()> {
    if !pinned && password.is_empty() {
        return Err(Error::ServerTrustRequired);
    }
    let mode = read::<1>(conn, deadline, cancel).await?[0];
    if mode == 0 && pinned && password.is_empty() {
        return Ok(());
    }
    if mode != 1 || password.is_empty() {
        return Err(Error::AuthenticationRejected);
    }
    let start = ClientLogin::<Suite>::start(&mut OsRng, password.as_bytes())
        .map_err(|_| Error::AuthenticationRejected)?;
    conn.control_tx
        .write(&start.message.serialize(), deadline, cancel)
        .await?;
    let input = read::<320>(conn, deadline, cancel).await?;
    let response = CredentialResponse::<Suite>::deserialize(&input)
        .map_err(|_| Error::AuthenticationRejected)?;
    let mut context = b"resona-server-password-v1\0".to_vec();
    context.extend_from_slice(conn.channel_binding());
    let mut finish = start
        .state
        .finish(
            &mut OsRng,
            password.as_bytes(),
            response,
            ClientLoginFinishParameters::new(
                Some(&context),
                Identifiers {
                    client: Some(b"resona-guest"),
                    server: Some(b"resona-server"),
                },
                None,
            ),
        )
        .map_err(|_| Error::AuthenticationRejected)?;
    finish.session_key.zeroize();
    finish.export_key.zeroize();
    conn.control_tx
        .write(&finish.message.serialize(), deadline, cancel)
        .await?;
    if read::<1>(conn, deadline, cancel).await? != [1] {
        return Err(Error::AuthenticationRejected);
    }
    Ok(())
}

async fn read<const N: usize>(
    conn: &mut Connection,
    deadline: Instant,
    cancel: &CancellationToken,
) -> Result<[u8; N]> {
    let mut bytes = [0; N];
    let mut offset = 0;
    while offset < N {
        let n = conn
            .control_rx
            .read(&mut bytes[offset..], deadline, cancel)
            .await?;
        if n == 0 {
            return Err(Error::InvalidState);
        }
        offset += n;
    }
    Ok(bytes)
}

# Environment Variables

## ComplexConfig

ComplexConfig is an example configuration structure.
It contains a few fields with different types of tags.
It is trying to cover all the possible cases.

 - `SECRET` (from-file) - Secret is a secret value that is read from a file.
 - `PASSWORD` (from-file, default: `/tmp/password`) - Password is a password that is read from a file.
 - `CERTIFICATE` (expand, from-file, default: `${CERTIFICATE_FILE}`) - Certificate is a certificate that is read from a file.
 - `SECRET_KEY` (**required**) - Key is a secret key.
 - `SECRET_VAL` (**required**, non-empty) - SecretVal is a secret value.
 - `HOSTS` (separated by `:`, **required**) - Hosts is a list of hosts.
 - `WORDS` (comma-separated, from-file, default: `one,two,three`) - Words is just a list of words.
 - `COMMENT` (**required**, default: `This is a comment.`) - Just a comment.
 - Anon is an anonymous structure.
   - `ANON_USER` (**required**) - User is a user name.
   - `ANON_PASS` (**required**) - Pass is a password.

## NextConfig

 - `MOUNT` (**required**) - Mount is a mount point.

## FieldNames

FieldNames uses field names as env names.

 - `QUUX` - Quux is a field with a tag.

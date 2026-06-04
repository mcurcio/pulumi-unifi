// init-mongo.js — runs ONCE, on a fresh /data/db volume, via Mongo's
// /docker-entrypoint-initdb.d hook (executed as the root user created from
// MONGO_INITDB_ROOT_USERNAME/PASSWORD).
//
// The linuxserver/unifi-network-application image authenticates to Mongo as
// MONGO_USER (=unifi) against MONGO_AUTHSOURCE (=admin), and the Network app
// uses three databases: the main store (unifi), the stats store (unifi_stat),
// and the audit store (unifi_audit). So we create one user on the admin db with
// dbOwner on all three.
//
// NOTE: this only fires when the data volume is empty. On a restore (restore.sh
// loads a mongodump into a fresh volume) the dump itself does not carry the
// admin-db user, so restore.sh re-creates this user after loading — see that
// script. Keep the credentials here in sync with MONGO_PASS in the compose file
// and the re-create in restore.sh.
db = db.getSiblingDB('admin');
db.createUser({
  user: 'unifi',
  pwd: 'unifipass',
  roles: [
    { role: 'dbOwner', db: 'unifi' },
    { role: 'dbOwner', db: 'unifi_stat' },
    { role: 'dbOwner', db: 'unifi_audit' },
  ],
});

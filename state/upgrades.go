// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"strconv"
	"strings"

	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/utils/set"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/names.v2"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mgo.v2/txn"

	"github.com/juju/juju/cloud"
	"github.com/juju/juju/environs/config"
)

var upgradesLogger = loggo.GetLogger("juju.state.upgrade")

// runForAllModelStates will run runner function for every model passing a state
// for that model.
func runForAllModelStates(st *State, runner func(st *State) error) error {
	models, closer := st.getCollection(modelsC)
	defer closer()

	var modelDocs []bson.M
	err := models.Find(nil).Select(bson.M{"_id": 1}).All(&modelDocs)
	if err != nil {
		return errors.Annotate(err, "failed to read models")
	}

	for _, modelDoc := range modelDocs {
		modelUUID := modelDoc["_id"].(string)
		envSt, err := st.ForModel(names.NewModelTag(modelUUID))
		if err != nil {
			return errors.Annotatef(err, "failed to open model %q", modelUUID)
		}
		defer envSt.Close()
		if err := runner(envSt); err != nil {
			return errors.Annotatef(err, "model UUID %q", modelUUID)
		}
	}
	return nil
}

// readBsonDField returns the value of a given field in a bson.D.
func readBsonDField(d bson.D, name string) (interface{}, bool) {
	for i := range d {
		field := &d[i]
		if field.Name == name {
			return field.Value, true
		}
	}
	return nil, false
}

// replaceBsonDField replaces a field in bson.D.
func replaceBsonDField(d bson.D, name string, value interface{}) error {
	for i, field := range d {
		if field.Name == name {
			newField := field
			newField.Value = value
			d[i] = newField
			return nil
		}
	}
	return errors.NotFoundf("field %q", name)
}

// RenameAddModelPermission renames any permissions called addmodel to add-model.
func RenameAddModelPermission(st *State) error {
	coll, closer := st.getRawCollection(permissionsC)
	defer closer()
	upgradesLogger.Infof("migrating addmodel permission")

	iter := coll.Find(bson.M{"access": "addmodel"}).Iter()
	defer iter.Close()
	var ops []txn.Op
	var doc bson.M
	for iter.Next(&doc) {
		id, ok := doc["_id"]
		if !ok {
			return errors.New("no id found in permission doc")
		}

		ops = append(ops, txn.Op{
			C:      permissionsC,
			Id:     id,
			Assert: txn.DocExists,
			Update: bson.D{{"$set", bson.D{{"access", "add-model"}}}},
		})
	}
	if err := iter.Err(); err != nil {
		return errors.Trace(err)
	}
	return st.runRawTransaction(ops)
}

// StripLocalUserDomain removes any @local suffix from any relevant document field values.
func StripLocalUserDomain(st *State) error {
	var ops []txn.Op
	more, err := stripLocalFromFields(st, cloudCredentialsC, "_id", "owner")
	if err != nil {
		return err
	}
	ops = append(ops, more...)

	more, err = stripLocalFromFields(st, modelsC, "owner", "cloud-credential")
	if err != nil {
		return err
	}
	ops = append(ops, more...)

	more, err = stripLocalFromFields(st, usermodelnameC, "_id")
	if err != nil {
		return err
	}
	ops = append(ops, more...)

	more, err = stripLocalFromFields(st, controllerUsersC, "_id", "user", "createdby")
	if err != nil {
		return err
	}
	ops = append(ops, more...)

	more, err = stripLocalFromFields(st, modelUsersC, "_id", "user", "createdby")
	if err != nil {
		return err
	}
	ops = append(ops, more...)

	more, err = stripLocalFromFields(st, permissionsC, "_id", "subject-global-key")
	if err != nil {
		return err
	}
	ops = append(ops, more...)

	more, err = stripLocalFromFields(st, modelUserLastConnectionC, "_id", "user")
	if err != nil {
		return err
	}
	ops = append(ops, more...)
	return st.runRawTransaction(ops)
}

func stripLocalFromFields(st *State, collName string, fields ...string) ([]txn.Op, error) {
	coll, closer := st.getRawCollection(collName)
	defer closer()
	upgradesLogger.Infof("migrating document fields of the %s collection", collName)

	iter := coll.Find(nil).Iter()
	defer iter.Close()
	var ops []txn.Op
	var doc bson.D
	for iter.Next(&doc) {
		// Get a copy of the current doc id so we can see if it has changed.
		var newId interface{}
		id, ok := readBsonDField(doc, "_id")
		if ok {
			newId = id
		}

		// Take a copy of the current doc fields.
		newDoc := make(bson.D, len(doc))
		for i, f := range doc {
			newDoc[i] = f
		}

		// Iterate over the fields that need to be updated and
		// record any updates to be made.
		var update bson.D
		for _, field := range fields {
			isId := field == "_id"
			fieldVal, ok := readBsonDField(doc, field)
			if !ok {
				continue
			}
			updatedVal := strings.Replace(fieldVal.(string), "@local", "", -1)
			if err := replaceBsonDField(newDoc, field, updatedVal); err != nil {
				return nil, err
			}
			if isId {
				newId = updatedVal
			} else {
				if fieldVal != updatedVal {
					update = append(update, bson.DocElem{
						"$set", bson.D{{field, updatedVal}},
					})
				}
			}
		}

		// For documents where the id has not changed, we can
		// use an update operation.
		if newId == id {
			if len(update) > 0 {
				ops = append(ops, txn.Op{
					C:      collName,
					Id:     id,
					Assert: txn.DocExists,
					Update: update,
				})
			}
		} else {
			// Where the id has changed, we need to remove the old and
			// insert the new document.
			ops = append(ops, []txn.Op{{
				C:      collName,
				Id:     id,
				Assert: txn.DocExists,
				Remove: true,
			}, {
				C:      collName,
				Id:     newId,
				Assert: txn.DocMissing,
				Insert: newDoc,
			}}...)
		}
	}
	if err := iter.Err(); err != nil {
		return nil, errors.Trace(err)
	}
	return ops, nil
}

func DropOldLogIndex(st *State) error {
	// If the log collection still has the old e,t index, remove it.
	key := []string{"e", "t"}
	db := st.MongoSession().DB(logsDB)
	collection := db.C(logsC)
	err := collection.DropIndex(key...)
	if err == nil {
		return nil
	}
	if queryErr, ok := err.(*mgo.QueryError); ok {
		if strings.HasPrefix(queryErr.Message, "index not found") {
			return nil
		}
	}
	return errors.Trace(err)
}

// AddMigrationAttempt adds an "attempt" field to migration documents
// which are missing one.
func AddMigrationAttempt(st *State) error {
	coll, closer := st.getRawCollection(migrationsC)
	defer closer()

	query := coll.Find(bson.M{"attempt": bson.M{"$exists": false}})
	query = query.Select(bson.M{"_id": 1})
	iter := query.Iter()
	defer iter.Close()
	var ops []txn.Op
	var doc bson.M
	for iter.Next(&doc) {
		id := doc["_id"]
		attempt, err := extractMigrationAttempt(id)
		if err != nil {
			upgradesLogger.Warningf("%s (skipping)", err)
			continue
		}

		ops = append(ops, txn.Op{
			C:      migrationsC,
			Id:     id,
			Assert: txn.DocExists,
			Update: bson.D{{"$set", bson.D{{"attempt", attempt}}}},
		})
	}
	if err := iter.Err(); err != nil {
		return errors.Annotate(err, "iterating migrations")
	}

	return errors.Trace(st.runRawTransaction(ops))
}

func extractMigrationAttempt(id interface{}) (int, error) {
	idStr, ok := id.(string)
	if !ok {
		return 0, errors.Errorf("invalid migration doc id type: %v", id)
	}

	_, attemptStr, ok := splitDocID(idStr)
	if !ok {
		return 0, errors.Errorf("invalid migration doc id: %v", id)
	}

	attempt, err := strconv.Atoi(attemptStr)
	if err != nil {
		return 0, errors.Errorf("invalid migration attempt number: %v", id)
	}

	return attempt, nil
}

// AddLocalCharmSequences creates any missing sequences in the
// database for tracking already used local charm revisions.
func AddLocalCharmSequences(st *State) error {
	charmsColl, closer := st.getRawCollection(charmsC)
	defer closer()

	query := bson.M{
		"url": bson.M{"$regex": "^local:"},
	}
	var docs []bson.M
	err := charmsColl.Find(query).Select(bson.M{
		"_id":  1,
		"life": 1,
	}).All(&docs)
	if err != nil {
		return errors.Trace(err)
	}

	// model UUID -> charm URL base -> max revision
	maxRevs := make(map[string]map[string]int)
	var deadIds []string
	for _, doc := range docs {
		id, ok := doc["_id"].(string)
		if !ok {
			upgradesLogger.Errorf("invalid charm id: %v", doc["_id"])
			continue
		}
		modelUUID, urlStr, ok := splitDocID(id)
		if !ok {
			upgradesLogger.Errorf("unable to split charm _id: %v", id)
			continue
		}
		url, err := charm.ParseURL(urlStr)
		if err != nil {
			upgradesLogger.Errorf("unable to parse charm URL: %v", err)
			continue
		}

		if _, exists := maxRevs[modelUUID]; !exists {
			maxRevs[modelUUID] = make(map[string]int)
		}

		baseURL := url.WithRevision(-1).String()
		curRev := maxRevs[modelUUID][baseURL]
		if url.Revision > curRev {
			maxRevs[modelUUID][baseURL] = url.Revision
		}

		if life, ok := doc["life"].(int); !ok {
			upgradesLogger.Errorf("invalid life for charm: %s", id)
			continue
		} else if life == int(Dead) {
			deadIds = append(deadIds, id)
		}

	}

	sequences, closer := st.getRawCollection(sequenceC)
	defer closer()
	for modelUUID, modelRevs := range maxRevs {
		for baseURL, maxRevision := range modelRevs {
			name := charmRevSeqName(baseURL)
			updater := newDbSeqUpdater(sequences, modelUUID, name)
			err := updater.ensure(maxRevision + 1)
			if err != nil {
				return errors.Annotatef(err, "setting sequence %s", name)
			}
		}

	}

	// Remove dead charm documents
	var ops []txn.Op
	for _, id := range deadIds {
		ops = append(ops, txn.Op{
			C:      charmsC,
			Id:     id,
			Remove: true,
		})
	}
	err = st.runRawTransaction(ops)
	return errors.Annotate(err, "removing dead charms")
}

// UpdateLegacyLXDCloudCredentials updates the cloud credentials for the
// LXD-based controller, and updates the cloud endpoint with the given
// value.
func UpdateLegacyLXDCloudCredentials(
	st *State,
	endpoint string,
	credential cloud.Credential,
) error {
	cloudOps, err := updateLegacyLXDCloudsOps(st, endpoint)
	if err != nil {
		return errors.Trace(err)
	}
	credOps, err := updateLegacyLXDCredentialsOps(st, credential)
	if err != nil {
		return errors.Trace(err)
	}
	return st.runTransaction(append(cloudOps, credOps...))
}

func updateLegacyLXDCloudsOps(st *State, endpoint string) ([]txn.Op, error) {
	clouds, err := st.Clouds()
	if err != nil {
		return nil, errors.Trace(err)
	}
	var ops []txn.Op
	for _, c := range clouds {
		if c.Type != "lxd" {
			continue
		}
		authTypes := []string{string(cloud.CertificateAuthType)}
		set := bson.D{{"auth-types", authTypes}}
		if c.Endpoint == "" {
			set = append(set, bson.DocElem{"endpoint", endpoint})
		}
		for _, region := range c.Regions {
			if region.Endpoint == "" {
				set = append(set, bson.DocElem{
					"regions." + region.Name + ".endpoint",
					endpoint,
				})
			}
		}
		upgradesLogger.Infof("updating cloud %q: %v", c.Name, set)
		ops = append(ops, txn.Op{
			C:      cloudsC,
			Id:     c.Name,
			Assert: txn.DocExists,
			Update: bson.D{{"$set", set}},
		})
	}
	return ops, nil
}

func updateLegacyLXDCredentialsOps(st *State, cred cloud.Credential) ([]txn.Op, error) {
	var ops []txn.Op
	coll, closer := st.getRawCollection(cloudCredentialsC)
	defer closer()
	iter := coll.Find(bson.M{"auth-type": "empty"}).Iter()
	var doc cloudCredentialDoc
	for iter.Next(&doc) {
		cloudCredentialTag, err := doc.cloudCredentialTag()
		if err != nil {
			upgradesLogger.Debugf("%v", err)
			continue
		}
		c, err := st.Cloud(doc.Cloud)
		if err != nil {
			return nil, errors.Trace(err)
		}
		if c.Type != "lxd" {
			continue
		}
		op := updateCloudCredentialOp(cloudCredentialTag, cred)
		upgradesLogger.Infof("updating credential %q: %v", cloudCredentialTag, op)
		ops = append(ops, op)
	}
	if err := iter.Err(); err != nil {
		return nil, errors.Trace(err)
	}
	return ops, nil
}

func upgradeNoProxy(np string) string {
	if np == "" {
		return "127.0.0.1,localhost,::1"
	}
	nps := set.NewStrings("127.0.0.1", "localhost", "::1")
	for _, i := range strings.Split(np, ",") {
		nps.Add(i)
	}
	// sorting is not a big overhead in this case and eases testing.
	return strings.Join(nps.SortedValues(), ",")
}

// UpgradeNoProxyDefaults changes the default values of no_proxy
// to hold localhost values as defaults.
func UpgradeNoProxyDefaults(st *State) error {
	var ops []txn.Op
	coll, closer := st.getRawCollection(settingsC)
	defer closer()
	iter := coll.Find(bson.D{}).Iter()
	var doc settingsDoc
	for iter.Next(&doc) {
		noProxyVal := doc.Settings[config.NoProxyKey]
		noProxy, ok := noProxyVal.(string)
		if !ok {
			continue
		}
		noProxy = upgradeNoProxy(noProxy)
		doc.Settings[config.NoProxyKey] = noProxy
		ops = append(ops,
			txn.Op{
				C:      settingsC,
				Id:     doc.DocID,
				Assert: txn.DocExists,
				Update: bson.M{"$set": bson.M{"settings": doc.Settings}},
			})
	}
	if len(ops) > 0 {
		return errors.Trace(st.runRawTransaction(ops))
	}
	return nil
}

// AddNonDetachableStorageMachineId sets the "machineid" field on
// volume and filesystem docs that are inherently bound to that
// machine.
func AddNonDetachableStorageMachineId(st *State) error {
	return runForAllModelStates(st, addNonDetachableStorageMachineId)
}

func addNonDetachableStorageMachineId(st *State) error {
	var ops []txn.Op
	volumes, err := st.volumes(
		bson.D{{"machineid", bson.D{{"$exists", false}}}},
	)
	if err != nil {
		return errors.Trace(err)
	}
	for _, v := range volumes {
		var pool string
		if v.doc.Info != nil {
			pool = v.doc.Info.Pool
		} else if v.doc.Params != nil {
			pool = v.doc.Params.Pool
		}
		detachable, err := isDetachableVolumePool(st, pool)
		if err != nil {
			return errors.Trace(err)
		}
		if detachable {
			continue
		}
		attachments, err := st.VolumeAttachments(v.VolumeTag())
		if err != nil {
			return errors.Trace(err)
		}
		if len(attachments) != 1 {
			// There should be exactly one attachment since the
			// filesystem is non-detachable, but be defensive
			// and leave the document alone if our expectations
			// are not met.
			continue
		}
		machineId := attachments[0].Machine().Id()
		ops = append(ops, txn.Op{
			C:      volumesC,
			Id:     v.doc.Name,
			Assert: txn.DocExists,
			Update: bson.D{{"$set", bson.D{
				{"machineid", machineId},
			}}},
		})
	}
	filesystems, err := st.filesystems(
		bson.D{{"machineid", bson.D{{"$exists", false}}}},
	)
	if err != nil {
		return errors.Trace(err)
	}
	for _, f := range filesystems {
		var pool string
		if f.doc.Info != nil {
			pool = f.doc.Info.Pool
		} else if f.doc.Params != nil {
			pool = f.doc.Params.Pool
		}
		if detachable, err := isDetachableFilesystemPool(st, pool); err != nil {
			return errors.Trace(err)
		} else if detachable {
			continue
		}
		attachments, err := st.FilesystemAttachments(f.FilesystemTag())
		if err != nil {
			return errors.Trace(err)
		}
		if len(attachments) != 1 {
			// There should be exactly one attachment since the
			// filesystem is non-detachable, but be defensive
			// and leave the document alone if our expectations
			// are not met.
			continue
		}
		machineId := attachments[0].Machine().Id()
		ops = append(ops, txn.Op{
			C:      filesystemsC,
			Id:     f.doc.DocID,
			Assert: txn.DocExists,
			Update: bson.D{{"$set", bson.D{
				{"machineid", machineId},
			}}},
		})
	}
	if len(ops) > 0 {
		return errors.Trace(st.runTransaction(ops))
	}
	return nil
}

// RemoveNilValueApplicationSettings removes any application setting
// key-value pairs from "settings" where value is nil.
func RemoveNilValueApplicationSettings(st *State) error {
	coll, closer := st.getRawCollection(settingsC)
	defer closer()
	iter := coll.Find(bson.M{"_id": bson.M{"$regex": "^.*:a#.*"}}).Iter()
	var ops []txn.Op
	var doc settingsDoc
	for iter.Next(&doc) {
		settingsChanged := false
		for key, value := range doc.Settings {
			if value != nil {
				continue
			}
			settingsChanged = true
			delete(doc.Settings, key)
		}
		if settingsChanged {
			ops = append(ops, txn.Op{
				C:      settingsC,
				Id:     doc.DocID,
				Assert: txn.DocExists,
				Update: bson.M{"$set": bson.M{"settings": doc.Settings}},
			})
		}
	}
	if len(ops) > 0 {
		return errors.Trace(st.runRawTransaction(ops))
	}
	return nil
}

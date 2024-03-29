// Copyright 2012-2014 EveryBitCounts Software Services Inc. All rights reserved.
// Use of this source code is governed by the GNU GPL v3 license, found in the LICENSE_GPL3 file.

// this package is concerned with the expression and management of runtime data (objects and values) 
// in the relish language.

// Abstraction of persistence service for relish data.

package data

import (
	. "relish/dbg"
)

type StatementGroup struct {
	Statements []*SqlStatement
}

type SqlStatement struct {
	Statement string
	Args []interface{}
}

/*
Return a new empty statement group.
*/
func Stmts() (group *StatementGroup) {
	group = &StatementGroup{}
	return
}

/*
Return a new statement group with a single sql statement in it.
*/
func Stmt(statement string) (group *StatementGroup) {
	group = &StatementGroup{}
	group.Add(statement)
	return
}

/*
Add a statement to a statement group.
*/
func (sg *StatementGroup) Add(statement string) {
   sg.Statements = append(sg.Statements, &SqlStatement{Statement: statement})
}

/*
Add an argument to the last-added statement in the statement group.
Must have at least one statement first before calling this.
*/
func (sg *StatementGroup) Arg(val interface{}) {
   stmt := sg.Statements[len(sg.Statements)-1]
   stmt.Args = append(stmt.Args, val)
}



func (sg *StatementGroup) Args(args []interface{}) {
   stmt := sg.Statements[len(sg.Statements)-1]
   if stmt.Args == nil {
      stmt.Args = args
   } else {
      for _,arg := range args {
         stmt.Args = append(stmt.Args, arg)         
      } 
   }
}


/*
Clears all arguments that have been added to the statement group.
Note: Only a single-statement statement group can be re-used with new args,
or more precisely, only the last statement in the statement group 
can have its arguments re-appended to after ClearArgs.
*/
func (sg *StatementGroup) ClearArgs() {
   for _,stmt := range sg.Statements {
      stmt.Args = nil
   }
}


type DB interface {
	 EnsureTypeTable(typ *RType) (err error)
	 ExecStatements(statementGroup *StatementGroup) (err error)
	 ExecStatement(statement string, args ...interface{}) (err error)	
	 PersistSetAttr(th InterpreterThread, obj RObject, attr *AttributeSpec, val RObject, attrHadValue bool) (err error)
	 PersistAddToAttr(th InterpreterThread, obj RObject, attr *AttributeSpec, val RObject, insertedIndex int) (err error)
	 PersistRemoveFromAttr(obj RObject, attr *AttributeSpec, val RObject, removedIndex int) (err error)
   PersistRemoveAttr(obj RObject, attr *AttributeSpec) (err error) 	
   PersistClearAttr(obj RObject, attr *AttributeSpec) (err error)
   PersistSetAttrElement(th InterpreterThread, obj RObject, attr *AttributeSpec, val RObject, index int) (err error) 
   
   PersistMapPut(th InterpreterThread, theMap Map, key RObject,val RObject, isNewKey bool) (err error)    
   // Note: Missing PersistRemoveFromMap  
   PersistSetCollectionElement(th InterpreterThread, coll IndexSettable, val RObject, index int) (err error)   
 	 PersistAddToCollection(th InterpreterThread, coll AddableCollection, val RObject, insertedIndex int) (err error)
 	 PersistRemoveFromCollection(coll RemovableCollection, val RObject, removedIndex int) (err error)
   PersistClearCollection(coll RemovableCollection) (err error)    
     
	 EnsurePersisted(th InterpreterThread, obj RObject) (err error)
	 EnsureAttributeAndRelationTables(t *RType) (err error)
	
	 ObjectNameExists(name string) (found bool, err error)
   ObjectNames(prefix string) (names []string, err error)  
	 NameObject(obj RObject, name string) (err error)
	 RenameObject(oldName string, newName string) (err error)	
	
   /*
   Deletes the object from the database as well as its canonical object name entry if any.

   Also removes the object from in-memory cache.
   TODO: Consider multiple in-memory caches when we have them!!

   Does NOT delete attribute / relation/ collection association table entries that may exist
   for the object in the database.

   Is a safe no-op for objects that are not stored locally.
   */
   Delete(obj RObject) (err error)
	 RecordPackageName(name string, shortName string)
	 FetchByName(name string, radius int) (obj RObject, err error)
	 Fetch(id int64, radius int) (obj RObject, err error)
   Refresh(obj RObject, radius int) (err error)
	 FetchAttribute(th InterpreterThread, objId int64, obj RObject, attr *AttributeSpec, radius int) (val RObject, err error)

	/*
	
	Given an object type and an OQL selection criteria clause in a string, set the argument collection to contain 
	the matching objects from the the database.

	e.g. of first two arguments: vehicles/Car, "speed > 60 order by speed desc"   

  If coll is non-nil, it is treated as a persistent collection that the selection conditions are filtering. 

	mayContainProxies will be true if the collection was fetched lazily from db.
	*/
	
    FetchN(typ *RType, oqlSelectionCriteria string, queryArgs []RObject, coll RCollection, radius int, objs *[]RObject) (mayContainProxies bool, err error) 

    /*
    Close the connection to the database.
    */
	 Close()


	/*
    Begins an immediate-mode database transaction.	
    Implementations may also first lock program access to the database to ensure a single goroutine at a time
    runs a database transaction and no other goroutines interact with the database at all during the transaction.
	*/
	 BeginTransaction() (err error)

	/*
    Implementations may also unlock program access to the database to allow other goroutines to use the database.
	*/
	 CommitTransaction() (err error)

	/*
    Implementations may also unlock program access to the database to allow other goroutines to use the database.
	*/
	 RollbackTransaction() (err error)
	
	/*
	Lock the dbMutex.
	Used to ensure exlusive access to db for single db reads / writes 
	for which we don't want to manually start a long-running transaction.
	(Or may also be used in multi-threaded extensions of the Begin,Commit,RollbackTransaction methods.)

	This method will block until no other goroutine is using the database.
	*/
	 UseDB()
	
	/*
	If this db connection or thread-of-connection has no further interest in owning the database,
	unlock the dbMutex.
	If this db connection or thread-of-connection still has an interest in owning the database,
	returns false. A series of calls to this should eventually return true meaning no further
	calls to it by this thread-of-connection are appropriate until UseDB() is called again.
	*/	
	 ReleaseDB() bool
}


/*
A single "thread" of execution of the relish interpreter, complete with its own relish data stack (hidden).
This interface provides access to the package whose method is currently executing, 
to the actual RMethod that is currently executing, and to the DBThread which can execute
database queries in a multi-threaded context. 

Note that each InterpreterThread actually corresponds to one goroutine. Goroutines are lightweight userspace
threads in Go which may be cooperative coroutines or may be mapped onto separate processor threads and/or cores.
Typically, multiple goroutines will execute in a single OS thread, but if multiple cores are available and
Go is configured to use them, then groups of goroutines may be apportioned across multiple OS-threads and cores. 
*/
type InterpreterThread interface {
	/*
	The package context of the executing method.
	*/
	Package() *RPackage
	
	/*
	The executing method.
	*/
	Method() *RMethod

  /*
  Returns the package from which the currently executing method was called, 
  or nil if at stack bottom.
  */
  CallingPackage() *RPackage 

  /*
  Returns the method that called the currently executing method, 
  or nil if at stack bottom.
  */
  CallingMethod() *RMethod


   /*
    A db connection thread. Used to serialize access to the database in a multi-threaded environment,
    and to manage database transactions.
  */
	DB() DB
	
	/*
	Will be "" unless we are in a stack-unrolling panic, in which case, should be the error message.
	*/
	Err() string
	
	// AllowGC(msg string)   // deadlock debug versions
	
	// DisallowGC(msg string)   // deadlock debug versions

  AllowGC()
  
  DisallowGC()
	
	GC()

	EvaluationContext() MethodEvaluationContext

  /*
  The currently active DB transaction which this thread started or is participating in.
  */
  Transaction() *RTransaction 

  SetTransaction(tx *RTransaction)

}

func NewDBThread(database DB) *DBThread {
   return &DBThread{db:database}
}

/*
    Has a reference to the DB. 
    Executes DB queries in a serialized fashion in a multi-threaded environment.
    Also manages database transactions.
*/
type DBThread struct {
	db DB  // the database connection-managing object

	acquiringDbLock bool  // This thread is in the process of acquiring and locking the dbMutex 
	                      // (but may still be blocked waiting for the mutex to be unlocked by another thread)
	
	dbLockOwnershipDepth int  // How many nested claims has this thread currently made for ownership of the dbMutex
                              // If > 0, this thread owns and has locked the dbMutex.
                              // Note: thread "ownership" of dbMutex is an abstract concept imposed by this DBThread s/w,
                              // because Go Mutexes are not inherently owned by any particular goroutine.	
}

/*
Grabs the dbMutex when it can (blocking until then) then executes a BEGIN IMMEDIATE TRANSACTION sql statement.
Does not unlock the dbMutex or release this thread's ownership of the mutex. 
Use CommitTransaction or RollbackTransaction to do that.
*/
func (dbt * DBThread) BeginTransaction() (err error) {
   Logln(PERSIST2_,"DBThread.BeginTransaction") 	
   dbt.UseDB()	
   err = dbt.db.BeginTransaction() 
   if err != nil {
   	   dbt.ReleaseDB()
   }
   return
}

/*
Executes a COMMIT TRANSACTION sql statement. If it succeeds, unlocks the dbMutex and releases this thread's ownership
of the mutex.
If it fails (returns a non-nil error), does not unlock the dbMutex or release this thread's ownership of the mutex.

In the error case, the correct behaviour is to either retry the commit, do a rollback, or just call ReleaseDB to
unlock the dbMutex and release this thread's ownership of the mutex.
*/
func (dbt * DBThread) CommitTransaction() (err error) {
    Logln(PERSIST2_,"DBThread.CommitTransaction") 		
	err = dbt.db.CommitTransaction()
	if err == nil {
	   dbt.ReleaseDB()
    }
   return
}

/*
Executes a ROLLBACK TRANSACTION sql statement. If it succeeds, unlocks the dbMutex and releases this thread's ownership
of the mutex.
If it fails (returns a non-nil error), does not unlock the dbMutex or release this thread's ownership of the mutex.

In the error case, the correct behaviour is to either retry the rollback, or just call ReleaseDB to
unlock the dbMutex and release this thread's ownership of the mutex.
*/
func (dbt * DBThread) RollbackTransaction() (err error) {
    Logln(PERSIST2_,"DBThread.RollbackTransaction") 	
	err = dbt.db.RollbackTransaction()
	if err == nil {
		dbt.ReleaseDB()
	}
	return
}

/*
If the thread does not already own the dbMutex, lock the mutex and
flag that this thread owns it.
Used to ensure exlusive access to db for single db reads / writes 
for which we don't want to manually start a long-running transaction.

This method will block until no other DBThread is using the database.
*/
func (dbt * DBThread) UseDB() {
   Logln(PERSIST2_,"DBThread.UseDB when ownership level is",dbt.dbLockOwnershipDepth) 		
   if dbt.acquiringDbLock {  // Umm, shouldn't this be impossible? The same thread is blocked further inside this method.
      return	
   }	
   if dbt.dbLockOwnershipDepth == 0 {
   	   dbt.acquiringDbLock = true
   	   dbt.db.UseDB()
       dbt.acquiringDbLock = false      	
   }
   dbt.dbLockOwnershipDepth++
   Logln(PERSIST2_,"DBThread.UseDB: Set ownership level to",dbt.dbLockOwnershipDepth)    
}

/*

Remove one level of interest of this thread in the dbMutex.
If we have lost all interest in it, and
if the thread owns the dbMutex, unlock the mutex and
flag that this thread no longer owns it.
Returns false if this thread still has an interest in and lock on the dbMutex.
*/	
func (dbt * DBThread) ReleaseDB() bool {
    Logln(PERSIST2_,"DBThread.ReleaseDB when ownership level is",dbt.dbLockOwnershipDepth) 		
    if dbt.dbLockOwnershipDepth > 0 {
	   dbt.dbLockOwnershipDepth--
       Logln(PERSIST2_,"DBThread.ReleaseDB: Set ownership level to",dbt.dbLockOwnershipDepth)  	
	   if dbt.dbLockOwnershipDepth == 0 {
		  dbt.db.ReleaseDB()	
	   } else {
	      return false	
	   }
    }	
    return true
}



func (dbt * DBThread) EnsureTypeTable(typ *RType) (err error) {
   dbt.UseDB()	
   err = dbt.db.EnsureTypeTable(typ)
   dbt.ReleaseDB()  
   return 
}

func (dbt * DBThread) ExecStatements(statementGroup *StatementGroup) (err error) {
   dbt.UseDB()
   err = dbt.db.ExecStatements(statementGroup)
   dbt.ReleaseDB()
   return
}

func (dbt * DBThread) ExecStatement(statement string, args ...interface{}) (err error) {
   dbt.UseDB()
   err = dbt.db.ExecStatement(statement, args...)
   dbt.ReleaseDB()
   return
}

func (dbt * DBThread) PersistSetAttr(th InterpreterThread, obj RObject, attr *AttributeSpec, val RObject, attrHadValue bool) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistSetAttr(th, obj, attr, val, attrHadValue)
   dbt.ReleaseDB()
   return
}

func (dbt * DBThread) PersistAddToAttr(th InterpreterThread, obj RObject, attr *AttributeSpec, val RObject, insertedIndex int) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistAddToAttr(th, obj, attr, val, insertedIndex)
   dbt.ReleaseDB()
   return 
}

func (dbt * DBThread) PersistRemoveFromAttr(obj RObject, attr *AttributeSpec, val RObject, removedIndex int) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistRemoveFromAttr(obj, attr, val, removedIndex)
   dbt.ReleaseDB()
   return   
}

func (dbt * DBThread) PersistRemoveAttr(obj RObject, attr *AttributeSpec) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistRemoveAttr(obj, attr)
   dbt.ReleaseDB() 
   return  
}

func (dbt * DBThread) PersistClearAttr(obj RObject, attr *AttributeSpec) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistClearAttr(obj, attr)
   dbt.ReleaseDB()
   return
}


func (dbt * DBThread) PersistSetAttrElement(th InterpreterThread, obj RObject, attr *AttributeSpec, val RObject, index int) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistSetAttrElement(th, obj, attr, val, index)
   dbt.ReleaseDB()
   return   
}



      
func (dbt * DBThread) PersistMapPut(th InterpreterThread, theMap Map, key RObject,val RObject, isNewKey bool) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistMapPut(th, theMap, key, val, isNewKey)
   dbt.ReleaseDB()
   return   
}
      
      
func (dbt * DBThread) PersistSetCollectionElement(th InterpreterThread, coll IndexSettable, val RObject, index int) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistSetCollectionElement(th, coll, val, index)
   dbt.ReleaseDB()
   return   
}
  
func (dbt * DBThread) PersistAddToCollection(th InterpreterThread, coll AddableCollection, val RObject, insertedIndex int) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistAddToCollection(th, coll, val, insertedIndex)
   dbt.ReleaseDB()
   return   
}

func (dbt * DBThread) PersistRemoveFromCollection(coll RemovableCollection, val RObject, removedIndex int) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistRemoveFromCollection(coll, val, removedIndex)
   dbt.ReleaseDB()
   return   
}

func (dbt * DBThread) PersistClearCollection(coll RemovableCollection) (err error) {
   dbt.UseDB()	
   err = dbt.db.PersistClearCollection(coll)
   dbt.ReleaseDB()
   return   
}








func (dbt * DBThread) EnsurePersisted(th InterpreterThread, obj RObject) (err error) {
   dbt.UseDB()	
   err = dbt.db.EnsurePersisted(th, obj)
   dbt.ReleaseDB() 
   return  
}

func (dbt * DBThread) EnsureAttributeAndRelationTables(t *RType) (err error) {
   dbt.UseDB()	
   err = dbt.db.EnsureAttributeAndRelationTables(t)
   dbt.ReleaseDB()
   return 
}

func (dbt * DBThread) ObjectNameExists(name string) (found bool, err error) {
   dbt.UseDB()
   found,err = dbt.db.ObjectNameExists(name)
   dbt.ReleaseDB()
   return
}

func (dbt * DBThread) ObjectNames(prefix string) (names []string, err error) {
   dbt.UseDB()
   names,err = dbt.db.ObjectNames(prefix)
   dbt.ReleaseDB()
   return  
}


func (dbt * DBThread) NameObject(obj RObject, name string) (err error) {
   dbt.UseDB()
   err = dbt.db.NameObject(obj, name)
   dbt.ReleaseDB() 
   return  
}

func (dbt * DBThread) RenameObject(oldName string, newName string) (err error) {
   dbt.UseDB()
   err = dbt.db.RenameObject(oldName, newName)
   dbt.ReleaseDB() 
   return  
}


func (dbt * DBThread)  Delete(obj RObject) (err error) {
   dbt.UseDB()
   err = dbt.db.Delete(obj)
   dbt.ReleaseDB() 
   return  
}


func (dbt * DBThread) RecordPackageName(name string, shortName string) {
   dbt.UseDB()
   dbt.db.RecordPackageName(name, shortName)
   dbt.ReleaseDB()
   return   	
}

func (dbt * DBThread) FetchByName(name string, radius int) (obj RObject, err error) {
   dbt.UseDB()
   obj, err = dbt.db.FetchByName(name, radius)   
   dbt.ReleaseDB() 
   return  
}

func (dbt * DBThread) Fetch(id int64, radius int) (obj RObject, err error) {
   dbt.UseDB()
   obj, err = dbt.db.Fetch(id, radius)
   dbt.ReleaseDB()  
   return 
}

func (dbt * DBThread) Refresh(obj RObject, radius int) (err error) {
   dbt.UseDB()
   err = dbt.db.Refresh(obj, radius)
   dbt.ReleaseDB()  
   return 
}


func (dbt * DBThread) FetchAttribute(th InterpreterThread, objId int64, obj RObject, attr *AttributeSpec, radius int) (val RObject, err error) {
   dbt.UseDB()
   val, err = dbt.db.FetchAttribute(th, objId, obj, attr, radius)
   dbt.ReleaseDB()  
   return 
}

/*
Given an object type and an OQL selection criteria clause in a string, set the argument collection to contain 
the matching objects from the the database.

e.g. of first two arguments: vehicles/Car, "speed > 60 order by speed desc"   
*/
	
func (dbt * DBThread) FetchN(typ *RType, oqlSelectionCriteria string, queryArgs []RObject, coll RCollection, radius int, objs *[]RObject) (mayContainProxies bool, err error) {
   dbt.UseDB()
   mayContainProxies, err = dbt.db.FetchN(typ, oqlSelectionCriteria, queryArgs, coll, radius, objs)
   dbt.ReleaseDB()	
   return
}

/*
Close the connection to the database.
*/
func (dbt * DBThread) Close() {
	dbt.db.Close()
}
